package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type splitOptions struct {
	minimum         int
	multiKillWindow int
	beforeSecs      int
	afterSecs       int
	fromMe          bool
}

type detectedMultiKillWindow struct {
	killerName    string
	killerNum     int
	killCount     int
	firstKillTime int
	lastKillTime  int
	matchTimeMs   int
}

type clipWindow struct {
	killerName    string
	killerNum     int
	killCount     int
	firstKillTime int
	lastKillTime  int
	matchTimeMs   int
	clipStart     int
	clipEnd       int
}

type activeClipWriter struct {
	path   string
	file   *os.File
	writer *demoFileWriter
	window clipWindow
}

type splitRuntime struct {
	out         io.Writer
	demoPath    string
	outputDir   string
	windows     []clipWindow
	nextWindow  int
	activeClips []*activeClipWriter
}

func runSplitMultikill(out io.Writer, _ io.Writer, options splitOptions, paths []string) error {
	outputDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current working directory: %w", err)
	}

	for _, path := range paths {
		windows, err := collectMultiKillWindows(path, options)
		if err != nil {
			return err
		}
		if len(windows) == 0 {
			fmt.Fprintf(out, "%s: no multikills found\n", path)
			continue
		}

		clipWindows := makeClipWindows(windows, options.beforeSecs*1000, options.afterSecs*1000)
		if err := splitDemoIntoClips(out, path, outputDir, clipWindows); err != nil {
			return err
		}
	}

	return nil
}

func collectMultiKillWindows(path string, options splitOptions) ([]detectedMultiKillWindow, error) {
	windows := make([]detectedMultiKillWindow, 0, 8)
	parser := newParser(io.Discard, parserOptions{
		multiKillMin:    options.minimum,
		multiKillWindow: options.multiKillWindow,
	})
	parser.onMultiKillWindow = func(window multiKillWindow) {
		if len(window.outputs) == 0 {
			return
		}
		first := window.outputs[0]
		last := window.outputs[len(window.outputs)-1]
		if options.fromMe && first.attackerNum != parser.clientNum {
			return
		}
		windows = append(windows, detectedMultiKillWindow{
			killerName:    first.attackerName,
			killerNum:     first.attackerNum,
			killCount:     len(window.outputs),
			firstKillTime: first.serverTime,
			lastKillTime:  last.serverTime,
			matchTimeMs:   first.matchTimeMs,
		})
	}

	if err := parser.parseFile(path); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	sort.Slice(windows, func(i, j int) bool {
		if windows[i].firstKillTime != windows[j].firstKillTime {
			return windows[i].firstKillTime < windows[j].firstKillTime
		}
		return windows[i].killerName < windows[j].killerName
	})

	return windows, nil
}

func makeClipWindows(windows []detectedMultiKillWindow, beforeMs int, afterMs int) []clipWindow {
	clips := make([]clipWindow, 0, len(windows))

	for _, window := range windows {
		clipStart := window.firstKillTime - beforeMs
		if clipStart < 0 {
			clipStart = 0
		}
		clips = append(clips, clipWindow{
			killerName:    window.killerName,
			killerNum:     window.killerNum,
			killCount:     window.killCount,
			firstKillTime: window.firstKillTime,
			lastKillTime:  window.lastKillTime,
			matchTimeMs:   window.matchTimeMs,
			clipStart:     clipStart,
			clipEnd:       window.lastKillTime + afterMs,
		})
	}

	return clips
}

func splitDemoIntoClips(out io.Writer, demoPath string, outputDir string, windows []clipWindow) error {
	runtime := &splitRuntime{
		out:       out,
		demoPath:  demoPath,
		outputDir: outputDir,
		windows:   windows,
	}

	parser := newParser(io.Discard, parserOptions{})
	parser.onSnapshot = runtime.handleSnapshot

	if err := parser.parseFile(demoPath); err != nil {
		_ = runtime.closeAll()
		return fmt.Errorf("%s: %w", demoPath, err)
	}

	if err := runtime.closeAll(); err != nil {
		return err
	}

	return nil
}

func (r *splitRuntime) handleSnapshot(parser *parser, snapshot *snapshotState) error {
	active := r.activeClips[:0]
	for _, clip := range r.activeClips {
		if snapshot.ServerTime > clip.window.clipEnd {
			if err := clip.close(); err != nil {
				return err
			}
			fmt.Fprintln(r.out, clip.path)
			continue
		}
		active = append(active, clip)
	}
	r.activeClips = active

	for r.nextWindow < len(r.windows) && r.windows[r.nextWindow].clipEnd < snapshot.ServerTime {
		r.nextWindow++
	}

	for r.nextWindow < len(r.windows) && r.windows[r.nextWindow].clipStart <= snapshot.ServerTime {
		clip, err := r.startClip(parser, snapshot, r.windows[r.nextWindow])
		if err != nil {
			return err
		}
		r.activeClips = append(r.activeClips, clip)
		r.nextWindow++
	}

	for _, clip := range r.activeClips {
		if err := clip.writer.writeSnapshot(parser, snapshot); err != nil {
			return fmt.Errorf("write %s snapshot at %d: %w", clip.path, snapshot.ServerTime, err)
		}
	}

	return nil
}

func (r *splitRuntime) startClip(parser *parser, snapshot *snapshotState, window clipWindow) (*activeClipWriter, error) {
	path, err := buildClipPath(r.outputDir, r.demoPath, window)
	if err != nil {
		return nil, err
	}

	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", path, err)
	}

	writer := newDemoFileWriter(file)
	if err := writer.writeGamestate(parser); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write %s gamestate: %w", path, err)
	}

	return &activeClipWriter{
		path:   path,
		file:   file,
		writer: writer,
		window: window,
	}, nil
}

func (r *splitRuntime) closeAll() error {
	for _, clip := range r.activeClips {
		if err := clip.close(); err != nil {
			return err
		}
		fmt.Fprintln(r.out, clip.path)
	}
	r.activeClips = nil
	return nil
}

func (w *activeClipWriter) close() error {
	if w.file == nil {
		return nil
	}

	if err := w.writer.writeEndMarker(); err != nil {
		_ = w.file.Close()
		w.file = nil
		return fmt.Errorf("write %s end marker: %w", w.path, err)
	}
	if err := w.file.Close(); err != nil {
		w.file = nil
		return fmt.Errorf("close %s: %w", w.path, err)
	}

	w.file = nil
	return nil
}

func buildClipPath(outputDir string, demoPath string, window clipWindow) (string, error) {
	baseName := filepath.Base(demoPath)
	extension := filepath.Ext(baseName)
	name := strings.TrimSuffix(baseName, extension)
	timestamp := formatClipTimestamp(window.matchTimeMs)
	killer := sanitizePathComponent(window.killerName)
	if killer == "" {
		killer = "unknown"
	}

	basePath := filepath.Join(outputDir, fmt.Sprintf("%s_%s_%s_%dkills%s",
		name, timestamp, killer, window.killCount, extension))
	return nextAvailablePath(basePath)
}

func nextAvailablePath(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return path, nil
		}
		return "", err
	}

	extension := filepath.Ext(path)
	base := strings.TrimSuffix(path, extension)
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d%s", base, suffix, extension)
		if _, err := os.Stat(candidate); err != nil {
			if os.IsNotExist(err) {
				return candidate, nil
			}
			return "", err
		}
	}
}

func formatClipTimestamp(matchTimeMs int) string {
	if matchTimeMs < 0 {
		matchTimeMs = 0
	}

	hours := matchTimeMs / 3600000
	minutes := (matchTimeMs / 60000) % 60
	seconds := (matchTimeMs / 1000) % 60

	return fmt.Sprintf("%02d_%02d_%02d", hours, minutes, seconds)
}

func sanitizePathComponent(value string) string {
	value = strings.ToLower(cleanName(value))
	if value == "" {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(value))
	lastUnderscore := false

	for i := 0; i < len(value); i++ {
		ch := value[i]
		switch {
		case ch >= 'a' && ch <= 'z':
			builder.WriteByte(ch)
			lastUnderscore = false
		case ch >= 'A' && ch <= 'Z':
			builder.WriteByte(ch)
			lastUnderscore = false
		case ch >= '0' && ch <= '9':
			builder.WriteByte(ch)
			lastUnderscore = false
		case ch == '-' || ch == '_':
			builder.WriteByte(ch)
			lastUnderscore = ch == '_'
		default:
			if lastUnderscore || builder.Len() == 0 {
				continue
			}
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}

	return strings.Trim(builder.String(), "_")
}
