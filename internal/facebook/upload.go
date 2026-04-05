// Package facebook handles chunked video upload to a Facebook Page via the
// Graph API.
package facebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	graphBase  = "https://graph.facebook.com/v21.0"
	uploadBase = "https://rupload.facebook.com/video-upload/v21.0"
	chunkSize  = 200 * 1024 * 1024 // 200 MB per chunk
)

// SermonMetadata carries video metadata.
type SermonMetadata struct {
	Title    string
	Verse    string
	Preacher string
}

// ProgressFunc receives status updates during upload.
type ProgressFunc func(step, message string)

type startResponse struct {
	VideoID     string `json:"video_id"`
	UploadURL   string `json:"upload_url"`
	StartOffset string `json:"start_offset"`
	EndOffset   string `json:"end_offset"`
}

type transferResponse struct {
	StartOffset string `json:"start_offset"`
	EndOffset   string `json:"end_offset"`
}

type finishResponse struct {
	Success bool `json:"success"`
}

// Upload performs a chunked video upload to a Facebook Page.
// accessToken must be a Page access token with pages_manage_posts permission.
func Upload(ctx context.Context, videoPath, pageID, accessToken string, meta SermonMetadata, progress ProgressFunc) error {
	f, err := os.Open(videoPath)
	if err != nil {
		return fmt.Errorf("facebook: open video: %w", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("facebook: stat video: %w", err)
	}
	fileSize := stat.Size()

	// --- Phase 1: Start upload session ---
	progress("facebook", "Preparing Creative Library folder…")
	folderID, err := getOrCreateCreativeFolder(ctx, pageID, accessToken)
	if err != nil {
		return fmt.Errorf("facebook: creative folder: %w", err)
	}

	progress("facebook", "Starting Facebook upload session…")
	startResp, err := startUpload(ctx, pageID, accessToken, fileSize, buildTitle(meta), folderID)
	if err != nil {
		return fmt.Errorf("facebook: start upload: %w", err)
	}

	// --- Phase 2: Transfer chunks ---
	progress("facebook", "Uploading video to Facebook…")
	videoID := startResp.VideoID
	startOffset, _ := strconv.ParseInt(startResp.StartOffset, 10, 64)
	endOffset, _ := strconv.ParseInt(startResp.EndOffset, 10, 64)

	for startOffset < fileSize {
		chunkLen := endOffset - startOffset
		if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
			return fmt.Errorf("facebook: seek: %w", err)
		}
		chunk := make([]byte, chunkLen)
		if _, err := io.ReadFull(f, chunk); err != nil {
			return fmt.Errorf("facebook: read chunk: %w", err)
		}

		pct := int(float64(endOffset) / float64(fileSize) * 100)
		progress("facebook", fmt.Sprintf("Uploading… %d%%", pct))

		next, err := transferChunk(ctx, videoID, accessToken, chunk, startOffset, fileSize)
		if err != nil {
			return fmt.Errorf("facebook: transfer chunk at offset %d: %w", startOffset, err)
		}

		newStart, _ := strconv.ParseInt(next.StartOffset, 10, 64)
		newEnd, _ := strconv.ParseInt(next.EndOffset, 10, 64)
		if newStart == startOffset {
			// No progress — server signals done
			break
		}
		startOffset = newStart
		endOffset = newEnd
	}

	// --- Phase 3: Finish upload ---
	progress("facebook", "Finalizing Facebook upload…")
	description := buildDescription(meta)
	if err := finishUpload(ctx, pageID, videoID, accessToken, buildTitle(meta), description); err != nil {
		return fmt.Errorf("facebook: finish upload: %w", err)
	}

	progress("facebook", fmt.Sprintf("Facebook upload complete — Video ID: %s", videoID))
	return nil
}

func startUpload(ctx context.Context, pageID, accessToken string, fileSize int64, title, creativeFolderID string) (*startResponse, error) {
	endpoint := fmt.Sprintf("%s/%s/videos", graphBase, pageID)
	params := url.Values{
		"upload_phase": {"start"},
		"file_size":    {strconv.FormatInt(fileSize, 10)},
		"title":        {title},
		"access_token": {accessToken},
	}
	if creativeFolderID != "" {
		params.Set("creative_folder_id", creativeFolderID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}

	var result startResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse start response: %w\nbody: %s", err, body)
	}
	return &result, nil
}

func transferChunk(ctx context.Context, videoID, accessToken string, chunk []byte, startOffset, fileSize int64) (*transferResponse, error) {
	endpoint := fmt.Sprintf("%s/%s", uploadBase, videoID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(chunk))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "OAuth "+accessToken)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("offset", strconv.FormatInt(startOffset, 10))
	req.Header.Set("file_size", strconv.FormatInt(fileSize, 10))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}

	var result transferResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse transfer response: %w\nbody: %s", err, body)
	}
	return &result, nil
}

func finishUpload(ctx context.Context, pageID, videoID, accessToken, title, description string) error {
	endpoint := fmt.Sprintf("%s/%s/videos", graphBase, pageID)
	params := url.Values{
		"upload_phase": {"finish"},
		"video_id":     {videoID},
		"title":        {title},
		"description":  {description},
		"access_token": {accessToken},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}

	var result finishResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("parse finish response: %w\nbody: %s", err, body)
	}
	if !result.Success {
		return fmt.Errorf("facebook finish upload returned success=false")
	}
	return nil
}

// getOrCreateCreativeFolder returns the ID of the first existing creative
// folder on the page, creating a "Sermon Videos" folder if none exist.
// The Facebook video upload API (v21.0+) requires creative_folder_id.
func getOrCreateCreativeFolder(ctx context.Context, pageID, accessToken string) (string, error) {
	// Try to list existing folders.
	listURL := fmt.Sprintf("%s/%s/ad_creative_folders?fields=id,name&access_token=%s",
		graphBase, pageID, url.QueryEscape(accessToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// If the endpoint is not supported for this page type, proceed without a folder.
	if resp.StatusCode == http.StatusBadRequest && strings.Contains(string(body), "Unknown path components") {
		return "", nil
	}
	if resp.StatusCode == http.StatusOK {
		var listed struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &listed); err == nil && len(listed.Data) > 0 {
			return listed.Data[0].ID, nil
		}
	}
	// Create a new folder.
	params := url.Values{
		"name":         {"Sermon Videos"},
		"access_token": {accessToken},
	}
	createURL := fmt.Sprintf("%s/%s/ad_creative_folders", graphBase, pageID)
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, strings.NewReader(params.Encode()))
	if err != nil {
		return "", err
	}
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		return "", err
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	// If creating is also unsupported, proceed without a folder.
	if resp2.StatusCode == http.StatusBadRequest && strings.Contains(string(body2), "Unknown path components") {
		return "", nil
	}
	if resp2.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp2.StatusCode, body2)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body2, &created); err != nil || created.ID == "" {
		return "", fmt.Errorf("unexpected response creating folder: %s", body2)
	}
	return created.ID, nil
}

func buildTitle(meta SermonMetadata) string {
	return fmt.Sprintf("%s | %s", meta.Title, meta.Preacher)
}

func buildDescription(meta SermonMetadata) string {
	return fmt.Sprintf(
		"%s | %s | %s\n\nJoin us as %s brings a message from %s.\n\n#sermon #church #faith",
		meta.Title, meta.Preacher, meta.Verse, meta.Preacher, meta.Verse,
	)
}

// TestAuth verifies the page access token by fetching the page's public name.
// Returns the page name on success — no data is written.
func TestAuth(ctx context.Context, pageID, accessToken string, progress ProgressFunc) (string, error) {
	if pageID == "" || accessToken == "" {
		return "", fmt.Errorf("Facebook Page ID and Access Token must be set in Settings")
	}
	progress("facebook", "Verifying Facebook token…")
	endpoint := fmt.Sprintf("%s/%s?fields=name,id&access_token=%s", graphBase, pageID, url.QueryEscape(accessToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		// Error 190/492 means the token is valid but the account is not an
		// admin/editor/moderator of the page, or the token was generated by
		// a user without page-management permissions.
		// Guide the user to generate a proper Page Access Token.
		if strings.Contains(string(body), `"code":190`) {
			return "", fmt.Errorf("access token rejected by Facebook (error 190): " +
				"make sure you are an Admin / Editor / Moderator of the page and " +
				"that the access token is a Page Access Token (not a User token). " +
				"Generate one via Graph API Explorer with the pages_manage_posts and " +
				"pages_read_engagement permissions")
		}
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	var result struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	return result.Name, nil
}
