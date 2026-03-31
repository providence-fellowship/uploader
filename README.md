# Sermon Uploader

A desktop app for automating the upload of church sermon recordings to YouTube, Facebook, and Spotify — built with [Wails](https://wails.io) (Go + Svelte).

## What it does

After each service, point the app at your OBS recording and it will:

1. **Normalize** the audio to -16 LUFS (EBU R128, 2-pass ffmpeg) while keeping the video stream intact
2. **Upload to YouTube** via the YouTube Data API v3 (OAuth 2.0)
3. **Upload to Facebook** via the Graph API (chunked upload to your Page)
4. **Publish to Spotify for Creators** via browser automation (Playwright)

All three uploads run in sequence from a single click.

## Prerequisites

- [Go 1.21+](https://go.dev/dl/)
- [Wails v2](https://wails.io/docs/gettingstarted/installation) — `go install github.com/wailsapp/wails/v2/cmd/wails@latest`
- [Node.js](https://nodejs.org/) (for the Svelte frontend)
- [ffmpeg](https://ffmpeg.org/download.html) — must be on PATH or installed via WinGet/Chocolatey/Scoop
- [Playwright Chromium](https://playwright.dev/) — installed automatically on first run via `playwright.Install()`

## Setup

### 1. YouTube

1. Create a project in [Google Cloud Console](https://console.cloud.google.com/)
2. Enable the **YouTube Data API v3**
3. Create an **OAuth 2.0 Client ID** (Desktop app type)
4. Add `http://localhost:9876/oauth2callback` as an authorised redirect URI
5. Add your Google account as a **Test user** under OAuth consent screen
6. Paste the Client ID and Secret into Settings, then click **Authenticate YouTube**

### 2. Facebook

1. Go to [Meta Business Suite](https://business.facebook.com) → Settings → System Users
2. Create a system user with **Admin** role, assign your Page with Full control
3. Generate a token with `pages_manage_posts` and `pages_read_engagement` permissions, expiry **Never**
4. Paste the token and your Page ID into Settings

### 3. Spotify

1. Create a [Spotify for Creators](https://creators.spotify.com) account and set up your show
2. Paste your Spotify login email, password, and show URL into Settings
3. On first run the browser will open — log in and complete any 2FA. Subsequent runs reuse the saved session automatically.

## Running

```bash
# Development (hot reload)
wails dev

# Production build
wails build
```

On first launch, open **Settings** and fill in all credentials before starting an upload.

## Usage

1. Click **Select file** and choose your OBS recording (`.mp4`, `.mkv`, or `.mov`)
2. Fill in the **Title**, **Scripture verse**, and **Preacher** fields
3. Click **Start Upload** — progress for each platform is shown in real time
4. Use **Test Connections** to verify all credentials without posting anything
