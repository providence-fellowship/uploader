// Package spotify uses Playwright to automate uploading a podcast episode to
// Spotify for Creators (creators.spotify.com). There is no public API.
package spotify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
)

// SermonMetadata carries episode metadata.
type SermonMetadata struct {
	Title    string
	Verse    string
	Preacher string
}

// ProgressFunc receives status updates.
type ProgressFunc func(step, message string)

// Credentials holds Spotify login credentials.
type Credentials struct {
	Email    string
	Password string
	ShowURL  string // e.g. https://podcasters.spotify.com/pod/show/your-show-id
}

// browserProfileDir returns the path to the persistent Chromium profile used
// for Spotify. Using a persistent context means Spotify's session cookies are
// saved between runs — after the first successful login the CAPTCHA is never
// shown again.
func browserProfileDir() (string, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	dir := filepath.Join(cfgDir, "sermon-uploader", "spotify-profile")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create profile dir: %w", err)
	}
	return dir, nil
}

// launchPersistentContext starts a Chromium browser using a persistent user-data
// directory (so cookies survive between runs) and applies stealth args to
// prevent bot-detection CAPTCHAs.
func launchPersistentContext(pw *playwright.Playwright) (playwright.BrowserContext, error) {
	profileDir, err := browserProfileDir()
	if err != nil {
		return nil, err
	}
	ctx, err := pw.Chromium.LaunchPersistentContext(profileDir,
		playwright.BrowserTypeLaunchPersistentContextOptions{
			Headless: playwright.Bool(false),
			Args: []string{
				"--disable-blink-features=AutomationControlled",
				"--no-sandbox",
				"--disable-infobars",
				// Prevent startup dialogs (first-run wizard, crash-restore bubble)
				// that can close freshly created pages before our Goto() fires.
				"--no-first-run",
				"--no-default-browser-check",
				"--disable-session-crashed-bubble",
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("launch persistent context: %w", err)
	}
	return ctx, nil
}

// getOrCreatePage reuses a page that Chromium restored from the previous
// session, or opens a fresh one. This avoids the Target.createTarget error
// that can occur when closing all existing pages before calling NewPage().
func getOrCreatePage(ctx playwright.BrowserContext) (playwright.Page, error) {
	if pages := ctx.Pages(); len(pages) > 0 {
		return pages[0], nil
	}
	return ctx.NewPage()
}

// Upload logs into Spotify for Creators and creates a new episode with the
// given media file (video or audio). The browser window is shown (headless=false)
// so the user can handle 2FA / CAPTCHA interactively on first run.
func Upload(ctx context.Context, mediaPath string, creds Credentials, meta SermonMetadata, progress ProgressFunc) error {
	progress("spotify", "Launching browser for Spotify…")

	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("spotify: start playwright: %w", err)
	}
	defer pw.Stop()

	browserCtx, err := launchPersistentContext(pw)
	if err != nil {
		return fmt.Errorf("spotify: %w", err)
	}
	defer browserCtx.Close()

	// Inject stealth script before any navigation
	if err := browserCtx.AddInitScript(playwright.Script{
		Content: playwright.String(`Object.defineProperty(navigator,'webdriver',{get:()=>undefined})`)}); err != nil {
		progress("spotify", "Note: stealth script injection failed (non-fatal)")
	}

	page, err := getOrCreatePage(browserCtx)
	if err != nil {
		return fmt.Errorf("spotify: new page: %w", err)
	}

	// --- Login ---
	if err := loginToSpotify(page, creds, progress); err != nil {
		return fmt.Errorf("spotify: %w", err)
	}

	// --- Click "New episode" ---
	progress("spotify", "Opening new episode form…")
	if err := page.Click("[data-testid='new-episode-button']", playwright.PageClickOptions{
		Timeout: playwright.Float(15000),
	}); err != nil {
		return fmt.Errorf("spotify: click new episode button: %w", err)
	}
	_ = page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	})

	// --- Upload media file ---
	progress("spotify", "Selecting media file…")
	absMediaPath, err := filepath.Abs(mediaPath)
	if err != nil {
		return fmt.Errorf("spotify: resolve media path: %w", err)
	}
	// The file input is hidden inside the upload area wrapper.
	if err := page.SetInputFiles(
		"[data-testid='uploadAreaWrapper'] input[type='file']",
		absMediaPath,
	); err != nil {
		return fmt.Errorf("spotify: set media file: %w", err)
	}

	// --- Wait for upload to complete ---
	// The "Next" button is disabled while the upload is in progress; wait up
	// to 10 minutes for it to become enabled.
	progress("spotify", "Uploading media… (this may take several minutes)")
	if _, err := page.WaitForSelector(
		"button:has-text('Next'):not([disabled])",
		playwright.PageWaitForSelectorOptions{Timeout: playwright.Float(600000)},
	); err != nil {
		return fmt.Errorf("spotify: upload timed out waiting for Next button: %w", err)
	}

	// --- Fill title ---
	progress("spotify", "Filling episode details…")
	if err := page.Fill("[aria-label='Title (required)']", buildTitle(meta)); err != nil {
		return fmt.Errorf("spotify: fill title: %w", err)
	}

	// --- Fill description (rich-text editor — use keyboard for bold) ---
	// Click the paragraph inside the Episode info region to focus the editor.
	_ = page.Click("[role='region'][aria-label='Episode info'] p", playwright.PageClickOptions{
		Timeout: playwright.Float(5000),
	})
	// Clear any existing placeholder text.
	if err := page.Keyboard().Press("Control+A"); err == nil {
		_ = page.Keyboard().Press("Delete")
	}
	// Bold on → preacher name → bold off → " - Verse"
	_ = page.Keyboard().Press("Control+B")
	_ = page.Keyboard().Type(meta.Preacher)
	_ = page.Keyboard().Press("Control+B")
	_ = page.Keyboard().Type(fmt.Sprintf(" - %s", meta.Verse))

	// --- Next (episode details → publish settings) ---
	progress("spotify", "Proceeding to publish settings…")
	if err := page.Click("button:has-text('Next')", playwright.PageClickOptions{
		Timeout: playwright.Float(10000),
	}); err != nil {
		return fmt.Errorf("spotify: click next: %w", err)
	}

	// Select the first visibility radio (Public).
	_ = page.Click(".e-10100-form-radio__indicator", playwright.PageClickOptions{
		Timeout: playwright.Float(5000),
	})

	// --- Publish ---
	progress("spotify", "Publishing episode…")
	// Wait for the Publish button to be enabled (upload must be 100% first).
	if _, err := page.WaitForSelector(
		"button:has-text('Publish'):not([disabled])",
		playwright.PageWaitForSelectorOptions{Timeout: playwright.Float(30000)},
	); err != nil {
		return fmt.Errorf("spotify: publish button not ready: %w", err)
	}
	if err := page.Click("button:has-text('Publish')", playwright.PageClickOptions{
		Timeout: playwright.Float(10000),
	}); err != nil {
		return fmt.Errorf("spotify: click publish: %w", err)
	}

	progress("spotify", "Episode published successfully on Spotify!")
	_ = ctx
	time.Sleep(3 * time.Second)
	return nil
}

// loginToSpotify navigates to creators.spotify.com and logs in using the exact
// flow recorded via Playwright codegen. If the persistent browser context
// already has a valid session (cookies still good), it skips login entirely.
//
// Flow (when login is needed):
//  1. creators.spotify.com landing page → click "Log in"
//  2. Click "Continue with Spotify" → redirects to accounts.spotify.com
//  3. Fill email → click login-button → click "Log in with a password"
//  4. Fill password → submit → wait for dashboard redirect
func loginToSpotify(page playwright.Page, creds Credentials, progress ProgressFunc) error {
	progress("spotify", "Navigating to Spotify for Creators…")
	if _, err := page.Goto("https://creators.spotify.com/", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
	}); err != nil {
		return fmt.Errorf("navigate to creators: %w", err)
	}

	// Try to click "Log in". If it doesn't exist within 6s, we're already
	// authenticated — return early.
	progress("spotify", "Checking session…")
	_ = page.Click("a:has-text('Log in')", playwright.PageClickOptions{
		Timeout: playwright.Float(6000),
	})

	// After the click (or timeout), wait for navigation to settle and then
	// check where we ended up. If we're NOT on accounts.spotify.com, the
	// session cookie is still valid and we're already on the dashboard.
	_ = page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	})
	if !strings.Contains(page.URL(), "accounts.spotify.com") {
		progress("spotify", "Already logged in (session cookie valid)")
		return nil
	}

	// We're on accounts.spotify.com — click "Continue with Spotify" to reach
	// the credential entry form.
	progress("spotify", "Clicking Continue with Spotify…")
	if err := page.Click("button:has-text('Continue with Spotify')", playwright.PageClickOptions{
		Timeout: playwright.Float(10000),
	}); err != nil {
		return fmt.Errorf("click continue with spotify: %w", err)
	}

	if err := page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		State: playwright.LoadStateNetworkidle,
	}); err != nil {
		return fmt.Errorf("wait for accounts page: %w", err)
	}

	// Fill email
	progress("spotify", "Entering email…")
	if err := page.Fill("[data-testid='login-username']", creds.Email); err != nil {
		return fmt.Errorf("fill email: %w", err)
	}

	// Submit username → Spotify shows "Log in with a password" button
	if err := page.Click("[data-testid='login-button']", playwright.PageClickOptions{
		Timeout: playwright.Float(10000),
	}); err != nil {
		return fmt.Errorf("click next after email: %w", err)
	}

	// Click "Log in with a password" to reveal the password field
	progress("spotify", "Selecting password login…")
	if err := page.Click("button:has-text('Log in with a password')", playwright.PageClickOptions{
		Timeout: playwright.Float(10000),
	}); err != nil {
		return fmt.Errorf("click log in with a password: %w", err)
	}

	// Fill password
	progress("spotify", "Entering password…")
	if err := page.Fill("[data-testid='login-password']", creds.Password); err != nil {
		return fmt.Errorf("fill password: %w", err)
	}

	// Submit credentials
	if err := page.Click("[data-testid='login-button']", playwright.PageClickOptions{
		Timeout: playwright.Float(10000),
	}); err != nil {
		return fmt.Errorf("click submit: %w", err)
	}

	// Wait for redirect back to creators.spotify.com (2 min for 2FA / CAPTCHA).
	// The glob **creators.spotify.com* matches the root URL and any sub-path.
	progress("spotify", "Waiting for login… (handle 2FA in the browser if prompted)")
	if err := page.WaitForURL("**creators.spotify.com*", playwright.PageWaitForURLOptions{
		Timeout: playwright.Float(120000),
	}); err != nil {
		return fmt.Errorf("login timed out — did not reach dashboard: %w", err)
	}
	return nil
}

func buildTitle(meta SermonMetadata) string {
	return meta.Title
}

func buildDescription(meta SermonMetadata) string {
	return fmt.Sprintf("**%s** - %s", meta.Preacher, meta.Verse)
}

// TestAuth logs into Spotify for Podcasters and verifies the show URL is
// reachable. The browser is closed immediately after — nothing is created.
func TestAuth(ctx context.Context, creds Credentials, progress ProgressFunc) error {
	if creds.Email == "" || creds.Password == "" {
		return fmt.Errorf("Spotify email and password must be set in Settings")
	}
	if creds.ShowURL == "" {
		return fmt.Errorf("Spotify Show URL must be set in Settings")
	}

	progress("spotify", "Launching browser for Spotify auth check…")

	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("spotify: start playwright: %w", err)
	}
	defer pw.Stop()

	browserCtx, err := launchPersistentContext(pw)
	if err != nil {
		return fmt.Errorf("spotify: %w", err)
	}
	defer browserCtx.Close()

	if err := browserCtx.AddInitScript(playwright.Script{
		Content: playwright.String(`Object.defineProperty(navigator,'webdriver',{get:()=>undefined})`)}); err != nil {
		progress("spotify", "Note: stealth script injection failed (non-fatal)")
	}

	page, err := getOrCreatePage(browserCtx)
	if err != nil {
		return fmt.Errorf("spotify: new page: %w", err)
	}

	if err := loginToSpotify(page, creds, progress); err != nil {
		return fmt.Errorf("spotify: %w", err)
	}

	// Navigate to the show to confirm the URL is accessible
	progress("spotify", "Verifying show URL…")
	if _, err := page.Goto(creds.ShowURL, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
	}); err != nil {
		return fmt.Errorf("spotify: navigate to show: %w", err)
	}

	currentURL := page.URL()
	_ = ctx
	time.Sleep(1 * time.Second)

	if !strings.Contains(currentURL, "creators.spotify.com") && !strings.Contains(currentURL, "podcasters.spotify.com") {
		return fmt.Errorf("spotify: ended up at unexpected URL: %s", currentURL)
	}
	progress("spotify", "Spotify login verified — show dashboard reached")
	return nil
}
