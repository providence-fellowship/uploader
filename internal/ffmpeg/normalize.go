// Package ffmpeg wraps the ffmpeg CLI for audio normalization and extraction.
package ffmpeg

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ProgressFunc is called during encoding with a human-readable status string.
type ProgressFunc func(step, message string)

// loudnormStats are the measured values from ffmpeg pass 1.
type loudnormStats struct {
	InputI            string `json:"input_i"`
	InputTP           string `json:"input_tp"`
	InputLRA          string `json:"input_lra"`
	InputThresh       string `json:"input_thresh"`
	OutputI           string `json:"output_i"`
	OutputTP          string `json:"output_tp"`
	OutputLRA         string `json:"output_lra"`
	OutputThresh      string `json:"output_thresh"`
	NormalizationType string `json:"normalization_type"`
	TargetOffset      string `json:"target_offset"`
}

// findBinary searches PATH first, then well-known WinGet/Chocolatey install
// locations so the app works even when the installer didn't update the system
// PATH that the WebView2 host process inherits.
func findBinary(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	// Search common side-by-side install directories.
	candidateDirs := []string{
		// WinGet packages (per-user)
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "WinGet", "Packages"),
		// Chocolatey
		`C:\ProgramData\chocolatey\bin`,
		// Scoop
		filepath.Join(os.Getenv("USERPROFILE"), "scoop", "shims"),
		// Common manual installs
		`C:\ffmpeg\bin`,
		`C:\Program Files\ffmpeg\bin`,
		`C:\Program Files (x86)\ffmpeg\bin`,
	}
	exe := name + ".exe"
	for _, dir := range candidateDirs {
		if dir == "" {
			continue
		}
		// For WinGet the binary lives several subdirectories deep — walk one level.
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			candidate := filepath.Join(dir, e.Name(), "bin", exe)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
			// Some packages nest one level deeper (e.g. ffmpeg-8.1-full_build/bin)
			subs, _ := os.ReadDir(filepath.Join(dir, e.Name()))
			for _, sub := range subs {
				if !sub.IsDir() {
					continue
				}
				candidate2 := filepath.Join(dir, e.Name(), sub.Name(), "bin", exe)
				if _, err := os.Stat(candidate2); err == nil {
					return candidate2, nil
				}
			}
		}
		// Flat directory (Chocolatey, Scoop)
		flat := filepath.Join(dir, exe)
		if _, err := os.Stat(flat); err == nil {
			return flat, nil
		}
	}
	return "", fmt.Errorf("%s not found on PATH — please install ffmpeg and ensure it is accessible", name)
}

// CheckFFmpeg returns an error if ffmpeg cannot be located.
func CheckFFmpeg() error {
	_, err := findBinary("ffmpeg")
	return err
}

// Normalize runs a 2-pass EBU R128 loudness normalization to -16 LUFS on the
// input video file, copying the video stream unchanged. Returns the path to
// the normalized .mp4.
func Normalize(inputPath, outputDir string, progress ProgressFunc) (normalizedPath string, err error) {
	if err = CheckFFmpeg(); err != nil {
		return
	}

	base := strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
	normalizedPath = filepath.Join(outputDir, base+"_normalized.mp4")

	// --- Pass 1: measure loudness ---
	progress("normalize", "Pass 1/2: measuring loudness…")
	stats, err := measureLoudness(inputPath)
	if err != nil {
		return
	}

	// --- Pass 2: normalize, copy video stream ---
	progress("normalize", "Pass 2/2: normalizing audio…")
	filter := fmt.Sprintf(
		"loudnorm=I=-16:TP=-1.5:LRA=11:measured_I=%s:measured_LRA=%s:measured_TP=%s:measured_thresh=%s:offset=%s:linear=true",
		stats.InputI, stats.InputLRA, stats.InputTP, stats.InputThresh, stats.TargetOffset,
	)
	pass2Args := []string{
		"-y", "-i", inputPath,
		"-af", filter,
		"-c:v", "copy",
		"-c:a", "aac", "-b:a", "192k",
		normalizedPath,
	}
	if err = runFFmpeg(pass2Args, progress, "normalize"); err != nil {
		return
	}

	progress("normalize", "Normalization complete")
	return
}

// measureLoudness runs ffmpeg pass 1 and parses the loudnorm JSON from stderr.
func measureLoudness(inputPath string) (*loudnormStats, error) {
	ffmpegPath, err := findBinary("ffmpeg")
	if err != nil {
		return nil, err
	}
	args := []string{
		"-i", inputPath,
		"-af", "loudnorm=I=-16:TP=-1.5:LRA=11:print_format=json",
		"-f", "null", "-",
	}
	cmd := exec.Command(ffmpegPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Pass 1 always exits with code 1 when output is /dev/null; ignore.
	_ = cmd.Run()

	output := stderr.String()
	// Find the JSON object that loudnorm prints
	start := strings.LastIndex(output, "{")
	end := strings.LastIndex(output, "}")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("loudnorm: could not find JSON in ffmpeg output:\n%s", output)
	}
	jsonStr := output[start : end+1]

	var stats loudnormStats
	if err := json.Unmarshal([]byte(jsonStr), &stats); err != nil {
		return nil, fmt.Errorf("loudnorm: failed to parse stats JSON: %w", err)
	}
	return &stats, nil
}

// runFFmpeg executes an ffmpeg command and forwards stderr progress lines
// to the provided callback.
func runFFmpeg(args []string, progress ProgressFunc, step string) error {
	ffmpegPath, err := findBinary("ffmpeg")
	if err != nil {
		return err
	}
	cmd := exec.Command(ffmpegPath, args...)

	// ffmpeg writes progress to stderr
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "frame=") || strings.HasPrefix(line, "size=") || strings.HasPrefix(line, "time=") {
			progress(step, line)
		}
	}

	return cmd.Wait()
}

// ExtractAudio extracts audio from videoPath as an MP3 into outputDir and
// returns the path. Useful if the user provides an already-normalized file.
func ExtractAudio(videoPath, outputDir string, progress ProgressFunc) (string, error) {
	if err := CheckFFmpeg(); err != nil {
		return "", err
	}
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	audioPath := filepath.Join(outputDir, base+".mp3")

	_ = os.MkdirAll(outputDir, 0755)

	args := []string{
		"-y", "-i", videoPath,
		"-vn", "-c:a", "libmp3lame", "-q:a", "2",
		audioPath,
	}
	progress("extract", "Extracting audio…")
	if err := runFFmpeg(args, progress, "extract"); err != nil {
		return "", err
	}
	return audioPath, nil
}

// ProbeFile checks that ffmpeg is on PATH and that the given video file is
// readable and recognised as a valid media container. Returns a short
// description string (e.g. "ok — 1h02m34s, 1920x1080") on success.
func ProbeFile(videoPath string) (string, error) {
	if err := CheckFFmpeg(); err != nil {
		return "", err
	}
	if _, err := os.Stat(videoPath); err != nil {
		return "", fmt.Errorf("cannot read video file: %w", err)
	}

	// Use ffprobe to extract duration and resolution.
	ffprobePath, err := findBinary("ffprobe")
	if err != nil {
		// ffprobe not found — fall back to a simple existence check.
		return "file accessible (ffprobe not found, skipping format check)", nil
	}

	args := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height:format=duration",
		"-of", "json",
		videoPath,
	}
	out, err := exec.Command(ffprobePath, args...).Output()
	if err != nil {
		return "", fmt.Errorf("ffprobe error: %w", err)
	}

	var probe struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return "file readable (could not parse ffprobe output)", nil
	}

	desc := "file readable"
	if probe.Format.Duration != "" {
		// Convert seconds to hh:mm:ss
		var secs float64
		fmt.Sscanf(probe.Format.Duration, "%f", &secs) //nolint
		h := int(secs) / 3600
		m := (int(secs) % 3600) / 60
		s := int(secs) % 60
		desc = fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	if len(probe.Streams) > 0 {
		desc += fmt.Sprintf(", %dx%d", probe.Streams[0].Width, probe.Streams[0].Height)
	}
	return desc, nil
}
