// Package youtube handles OAuth 2.0 authentication and video upload to the
// YouTube Data API v3.
package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
	yt "google.golang.org/api/youtube/v3"

	"google.golang.org/api/option"
)

// SermonMetadata carries the information used to populate video metadata.
type SermonMetadata struct {
	Title    string
	Verse    string
	Preacher string
	Privacy  string // "unlisted" | "public" | "private"
}

// ProgressFunc is called periodically during upload.
type ProgressFunc func(step, message string)

// oauthConfig builds the oauth2 config from client credentials.
func oauthConfig(clientID, clientSecret string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		// YoutubeUploadScope  — needed to upload videos
		// YoutubeReadonlyScope — needed by channels.list (token verification)
		Scopes:      []string{yt.YoutubeUploadScope, yt.YoutubeReadonlyScope},
		Endpoint:    googleoauth.Endpoint,
		RedirectURL: "http://localhost:9876/oauth2callback",
	}
}

// Authenticate performs the OAuth 2.0 flow, saving the token to tokenPath.
// It opens the user's default browser and spins up a one-shot local HTTP
// server to receive the callback.
func Authenticate(clientID, clientSecret, tokenPath string) error {
	cfg := oauthConfig(clientID, clientSecret)

	// Generate a state token to prevent CSRF
	state := fmt.Sprintf("%d", time.Now().UnixNano())

	// ApprovalForce (prompt=consent) ensures Google re-presents the consent
	// screen even if a token already exists, so the newly-requested scopes are
	// included in the refreshed credentials.
	authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	fmt.Println("Opening browser for YouTube authentication…")
	fmt.Println("If the browser does not open, visit:\n", authURL)
	openURL(authURL)

	// One-shot local HTTP server to capture the auth code
	ln, err := net.Listen("tcp", "localhost:9876")
	if err != nil {
		return fmt.Errorf("could not start OAuth callback server: %w", err)
	}

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	srv := &http.Server{}
	srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "invalid state", http.StatusBadRequest)
			errCh <- errors.New("oauth: state mismatch")
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			errCh <- errors.New("oauth: no code in callback")
			return
		}
		fmt.Fprintln(w, "<html><body><h2>Authentication successful! You can close this tab.</h2></body></html>")
		codeCh <- code
	})

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	var code string
	select {
	case code = <-codeCh:
	case err = <-errCh:
		_ = srv.Close()
		return err
	case <-time.After(5 * time.Minute):
		_ = srv.Close()
		return errors.New("oauth: timed out waiting for browser callback")
	}
	_ = srv.Close()

	ctx := context.Background()
	token, err := cfg.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("oauth: token exchange failed: %w", err)
	}
	return saveToken(tokenPath, token)
}

// HasToken returns true if a token file exists at tokenPath.
func HasToken(tokenPath string) bool {
	_, err := os.Stat(tokenPath)
	return err == nil
}

// Upload uploads videoPath to YouTube using the stored token.
func Upload(ctx context.Context, videoPath, clientID, clientSecret, tokenPath string, meta SermonMetadata, progress ProgressFunc) error {
	cfg := oauthConfig(clientID, clientSecret)

	token, err := loadToken(tokenPath)
	if err != nil {
		return fmt.Errorf("youtube: load token: %w", err)
	}

	client := cfg.Client(ctx, token)
	svc, err := yt.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("youtube: create service: %w", err)
	}

	video := &yt.Video{
		Snippet: &yt.VideoSnippet{
			Title:       fmt.Sprintf("%s - %s", meta.Title, meta.Verse),
			Description: buildDescription(meta),
			Tags:        []string{"sermon", "church", meta.Preacher, meta.Verse},
			CategoryId:  "29", // Nonprofits & Activism
		},
		Status: &yt.VideoStatus{
			PrivacyStatus: meta.Privacy,
		},
	}

	f, err := os.Open(videoPath)
	if err != nil {
		return fmt.Errorf("youtube: open video: %w", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("youtube: stat video: %w", err)
	}
	totalBytes := stat.Size()

	progress("youtube", "Starting YouTube upload…")

	call := svc.Videos.Insert([]string{"snippet", "status"}, video)
	call.Media(f)

	// Track upload progress via the underlying HTTP transport progress
	call.Header().Set("X-Upload-Content-Length", fmt.Sprintf("%d", totalBytes))

	resp, err := call.Do()
	if err != nil {
		return fmt.Errorf("youtube: upload failed: %w", err)
	}

	progress("youtube", fmt.Sprintf("Upload complete — Video ID: %s", resp.Id))
	return nil
}

func buildDescription(meta SermonMetadata) string {
	return fmt.Sprintf("Sermon by %s", meta.Preacher)
}

func saveToken(path string, token *oauth2.Token) error {
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func loadToken(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}
	return &token, nil
}

// TestAuth verifies the stored OAuth token is valid by making a read-only
// channels.list call. Returns the authenticated channel name on success.
func TestAuth(ctx context.Context, clientID, clientSecret, tokenPath string, progress ProgressFunc) (string, error) {
	if clientID == "" || clientSecret == "" {
		return "", fmt.Errorf("YouTube Client ID / Secret not configured")
	}
	if !HasToken(tokenPath) {
		return "", fmt.Errorf("no token stored — please authenticate first via Settings")
	}

	cfg := oauthConfig(clientID, clientSecret)
	token, err := loadToken(tokenPath)
	if err != nil {
		return "", fmt.Errorf("load token: %w", err)
	}

	client := cfg.Client(ctx, token)
	svc, err := yt.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("create service: %w", err)
	}

	progress("youtube", "Verifying YouTube token…")
	resp, err := svc.Channels.List([]string{"snippet"}).Mine(true).Do()
	if err != nil {
		if strings.Contains(err.Error(), "insufficientPermissions") ||
			strings.Contains(err.Error(), "ACCESS_TOKEN_SCOPE_INSUFFICIENT") {
			// The stored token was issued before the readonly scope was added.
			// Deleting it forces the user to re-authenticate with the full scope set.
			_ = os.Remove(tokenPath)
			return "", fmt.Errorf("token scope outdated — stale token removed: please click \"Authenticate YouTube\" in Settings to re-authorise")
		}
		return "", fmt.Errorf("token check failed: %w", err)
	}
	if len(resp.Items) == 0 {
		return "", fmt.Errorf("token valid but no channels found for this account")
	}
	return resp.Items[0].Snippet.Title, nil
}
