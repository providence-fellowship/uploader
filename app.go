package main

import (
	"context"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"uploader/internal/config"
	"uploader/internal/facebook"
	"uploader/internal/ffmpeg"
	spotifypkg "uploader/internal/spotify"
	"uploader/internal/youtube"
)

// App is the Wails application struct. All exported methods are callable from
// the Svelte frontend via the generated bindings.
type App struct {
	ctx context.Context
}

// NewApp creates a new App.
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// ---------------------------------------------------------------------------
// Types shared with the frontend
// ---------------------------------------------------------------------------

// SermonMeta holds the metadata entered by the user in the UI.
type SermonMeta struct {
	Title    string `json:"title"`
	Verse    string `json:"verse"`
	Preacher string `json:"preacher"`
}

// UploadResult is returned by StartUpload.
type UploadResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------
// File dialog
// ---------------------------------------------------------------------------

// OpenVideoFileDialog opens a native file-picker filtered to video files.
// Returns the selected path or "" if cancelled.
func (a *App) OpenVideoFileDialog() string {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select OBS recording",
		Filters: []runtime.FileFilter{
			{DisplayName: "Video Files (*.mp4, *.mkv, *.mov)", Pattern: "*.mp4;*.mkv;*.mov"},
		},
	})
	if err != nil {
		return ""
	}
	return path
}

// ---------------------------------------------------------------------------
// Config / Settings
// ---------------------------------------------------------------------------

// LoadConfig returns the persisted configuration.
func (a *App) LoadConfig() config.Config {
	cfg, _ := config.Load()
	return cfg
}

// SaveConfig persists the provided configuration.
func (a *App) SaveConfig(cfg config.Config) error {
	return config.Save(cfg)
}

// HasYouTubeToken returns true if a YouTube OAuth token is already stored.
func (a *App) HasYouTubeToken() bool {
	tokenPath, err := config.TokenFilePath()
	if err != nil {
		return false
	}
	return youtube.HasToken(tokenPath)
}

// ---------------------------------------------------------------------------
// YouTube OAuth
// ---------------------------------------------------------------------------

// AuthenticateYouTube launches the OAuth flow to obtain a YouTube token.
// This opens the default browser; the user grants access and the token is
// saved automatically.
func (a *App) AuthenticateYouTube() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.YouTubeClientID == "" || cfg.YouTubeClientSecret == "" {
		return fmt.Errorf("YouTube Client ID and Client Secret must be set in Settings first")
	}
	tokenPath, err := config.TokenFilePath()
	if err != nil {
		return err
	}
	return youtube.Authenticate(cfg.YouTubeClientID, cfg.YouTubeClientSecret, tokenPath)
}

// ---------------------------------------------------------------------------
// Main upload pipeline
// ---------------------------------------------------------------------------

// StartUpload runs the full pipeline:
//  1. Normalize audio to -16 LUFS (ffmpeg)
//  2. Upload to YouTube (API)
//  3. Upload to Facebook (API)
//  4. Upload to Spotify (Playwright)
//
// Progress events are emitted to the frontend via Wails runtime events so the
// UI can update platform status cards in real time.
func (a *App) StartUpload(videoPath string, meta SermonMeta) UploadResult {
	cfg, err := config.Load()
	if err != nil {
		return UploadResult{OK: false, Message: "Failed to load config: " + err.Error()}
	}

	// emit sends a progress event to the frontend.
	emit := func(step, msg string) {
		runtime.EventsEmit(a.ctx, "upload:progress", map[string]string{
			"step":    step,
			"message": msg,
		})
	}

	// --- Step 1: ffmpeg normalization ---
	emit("normalize", "Starting audio normalization…")
	normalizedPath, err := ffmpeg.Normalize(videoPath, cfg.OutputDir, emit)
	if err != nil {
		emit("normalize", "ERROR: "+err.Error())
		return UploadResult{OK: false, Message: "Normalization failed: " + err.Error()}
	}
	// Clean up the intermediate file when the pipeline finishes.
	defer func() { _ = os.Remove(normalizedPath) }()

	// --- Step 2: YouTube ---
	emit("youtube", "Queued")
	tokenPath, _ := config.TokenFilePath()
	ytErr := youtube.Upload(a.ctx, normalizedPath, cfg.YouTubeClientID, cfg.YouTubeClientSecret, tokenPath,
		youtube.SermonMetadata{
			Title:    meta.Title,
			Verse:    meta.Verse,
			Preacher: meta.Preacher,
			Privacy:  cfg.YouTubePrivacy,
		}, emit)
	if ytErr != nil {
		emit("youtube", "ERROR: "+ytErr.Error())
	}

	// --- Step 3: Facebook ---
	emit("facebook", "Queued")
	fbErr := facebook.Upload(a.ctx, normalizedPath, cfg.FacebookPageID, cfg.FacebookAccessToken,
		facebook.SermonMetadata{
			Title:    meta.Title,
			Verse:    meta.Verse,
			Preacher: meta.Preacher,
		}, emit)
	if fbErr != nil {
		emit("facebook", "ERROR: "+fbErr.Error())
	}

	// --- Step 4: Spotify ---
	emit("spotify", "Queued")
	spErr := spotifypkg.Upload(a.ctx, normalizedPath,
		spotifypkg.Credentials{
			Email:    cfg.SpotifyEmail,
			Password: cfg.SpotifyPassword,
			ShowURL:  cfg.SpotifyShowURL,
		},
		spotifypkg.SermonMetadata{
			Title:    meta.Title,
			Verse:    meta.Verse,
			Preacher: meta.Preacher,
		}, emit)
	if spErr != nil {
		emit("spotify", "ERROR: "+spErr.Error())
	}

	// --- Summarize ---
	if ytErr != nil || fbErr != nil || spErr != nil {
		return UploadResult{
			OK:      false,
			Message: summarizeErrors(ytErr, fbErr, spErr),
		}
	}

	emit("done", "All uploads complete!")
	return UploadResult{OK: true, Message: "All uploads completed successfully!"}
}

func summarizeErrors(ytErr, fbErr, spErr error) string {
	msg := "Some uploads failed:"
	if ytErr != nil {
		msg += "\n• YouTube: " + ytErr.Error()
	}
	if fbErr != nil {
		msg += "\n• Facebook: " + fbErr.Error()
	}
	if spErr != nil {
		msg += "\n• Spotify: " + spErr.Error()
	}
	return msg
}

// ---------------------------------------------------------------------------
// Dry-run / connection test
// ---------------------------------------------------------------------------

// RunDryTest validates credentials and the video file without posting anything:
//   - ffmpeg: checks binary is on PATH and probes the video file (if provided)
//   - YouTube: calls channels.list to verify the stored token is still valid
//   - Facebook: fetches the page name to verify the access token
//   - Spotify: opens a browser, logs in, confirms the show URL is reachable, then closes
//
// Progress events are emitted to the same "upload:progress" channel so the
// existing platform status cards update in real time.
func (a *App) RunDryTest(videoPath string) UploadResult {
	cfg, err := config.Load()
	if err != nil {
		return UploadResult{OK: false, Message: "Failed to load config: " + err.Error()}
	}

	emit := func(step, msg string) {
		runtime.EventsEmit(a.ctx, "upload:progress", map[string]string{
			"step":    step,
			"message": msg,
		})
	}

	var errs []string

	// --- ffmpeg ---
	emit("normalize", "Checking ffmpeg…")
	if videoPath != "" {
		desc, err := ffmpeg.ProbeFile(videoPath)
		if err != nil {
			emit("normalize", "ERROR: "+err.Error())
			errs = append(errs, "ffmpeg: "+err.Error())
		} else {
			emit("normalize", "✓ ffmpeg ok — "+desc)
		}
	} else {
		if err := ffmpeg.CheckFFmpeg(); err != nil {
			emit("normalize", "ERROR: "+err.Error())
			errs = append(errs, "ffmpeg: "+err.Error())
		} else {
			emit("normalize", "✓ ffmpeg found on PATH (no file selected to probe)")
		}
	}

	// --- YouTube ---
	emit("youtube", "Testing YouTube auth…")
	tokenPath, _ := config.TokenFilePath()
	channelName, ytErr := youtube.TestAuth(a.ctx, cfg.YouTubeClientID, cfg.YouTubeClientSecret, tokenPath, emit)
	if ytErr != nil {
		emit("youtube", "ERROR: "+ytErr.Error())
		errs = append(errs, "YouTube: "+ytErr.Error())
	} else {
		emit("youtube", "✓ Authenticated — channel: "+channelName)
	}

	// --- Facebook ---
	emit("facebook", "Testing Facebook token…")
	pageName, fbErr := facebook.TestAuth(a.ctx, cfg.FacebookPageID, cfg.FacebookAccessToken, emit)
	if fbErr != nil {
		emit("facebook", "ERROR: "+fbErr.Error())
		errs = append(errs, "Facebook: "+fbErr.Error())
	} else {
		emit("facebook", "✓ Token valid — page: "+pageName)
	}

	// --- Spotify ---
	emit("spotify", "Testing Spotify login…")
	spErr := spotifypkg.TestAuth(a.ctx, spotifypkg.Credentials{
		Email:    cfg.SpotifyEmail,
		Password: cfg.SpotifyPassword,
		ShowURL:  cfg.SpotifyShowURL,
	}, emit)
	if spErr != nil {
		emit("spotify", "ERROR: "+spErr.Error())
		errs = append(errs, "Spotify: "+spErr.Error())
	} else {
		emit("spotify", "✓ Logged in and show dashboard verified")
	}

	if len(errs) > 0 {
		msg := "Dry run found issues:"
		for _, e := range errs {
			msg += "\n• " + e
		}
		return UploadResult{OK: false, Message: msg}
	}

	emit("done", "All checks passed!")
	return UploadResult{OK: true, Message: "All connections verified — ready to upload!"}
}
