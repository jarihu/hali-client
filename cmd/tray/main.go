package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/getlantern/systray"

	"hali/internal/buildinfo"
	"hali/internal/config"
	"hali/internal/notify"
	"hali/internal/winsvc"
)

const (
	dashboardURL   = "http://127.0.0.1:47433"
	healthEndpoint = "http://127.0.0.1:47433/api/health"
	statsEndpoint  = "http://127.0.0.1:47433/api/stats"
	pauseEndpoint  = "http://127.0.0.1:47433/api/pause"
	resumeEndpoint = "http://127.0.0.1:47433/api/resume"
	pollInterval   = 3 * time.Second
	backoff1       = 1 * time.Second
	backoff2       = 2 * time.Second
	backoffError   = 10 * time.Second
	maxFailsForRed = 3
)

// icon states
type iconState int

const (
	stateIdle iconState = iota
	stateSeeding
	stateDownloading
	stateError
	statePaused
)

var icons [5][]byte

func init() {
	icons[stateIdle] = solidICO(color.RGBA{100, 100, 100, 255})       // gray
	icons[stateSeeding] = solidICO(color.RGBA{74, 222, 128, 255})     // green
	icons[stateDownloading] = solidICO(color.RGBA{34, 211, 238, 255}) // cyan/blue
	icons[stateError] = solidICO(color.RGBA{248, 113, 113, 255})      // red
	icons[statePaused] = solidICO(color.RGBA{251, 191, 36, 255})      // amber
}

func main() {
	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(icons[stateIdle])
	systray.SetTooltip("Hali — starting…")

	// Menu items.
	mOpenDash := systray.AddMenuItem("Open Dashboard", "Open Hali dashboard in browser")
	systray.AddSeparator()
	mPause := systray.AddMenuItem("Pause Transfers", "Pause all uploads and downloads")
	mResume := systray.AddMenuItem("Resume Transfers", "Resume transfers")
	mResume.Hide()
	systray.AddSeparator()
	mOpenCache := systray.AddMenuItem("Open Cache Folder", "")
	mOpenLogs := systray.AddMenuItem("View Logs", "")
	systray.AddSeparator()
	mRestart := systray.AddMenuItem("Restart Service", "")
	mStartup := systray.AddMenuItem("Startup Settings…", "")
	systray.AddSeparator()
	mAbout := systray.AddMenuItem(fmt.Sprintf("About — v%s (%s)", buildinfo.Version, buildinfo.Commit), "")
	mAbout.Disable()
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Exit Tray", "Stop the tray app (service keeps running)")

	firstRun()

	go pollLoop()

	for {
		select {
		case <-mOpenDash.ClickedCh:
			openURL(dashboardURL)
		case <-mPause.ClickedCh:
			postAPI(pauseEndpoint)
			mPause.Hide()
			mResume.Show()
		case <-mResume.ClickedCh:
			postAPI(resumeEndpoint)
			mResume.Hide()
			mPause.Show()
		case <-mOpenCache.ClickedCh:
			openPath(filepath.Join(config.DataDir(), "cache"))
		case <-mOpenLogs.ClickedCh:
			openPath(filepath.Join(config.DataDir(), "logs"))
		case <-mRestart.ClickedCh:
			winsvc.StopService() //nolint:errcheck
			time.Sleep(2 * time.Second)
			winsvc.StartService() //nolint:errcheck
		case <-mStartup.ClickedCh:
			openURL("ms-settings:startupapps")
		case <-mQuit.ClickedCh:
			systray.Quit()
			return
		}
	}
}

func onExit() {}

// pollLoop polls /api/health and /api/stats, updating the icon and tooltip.
func pollLoop() {
	fails := 0
	hasSeenHealthy := false
	for {
		alive := checkHealth()
		if !alive {
			fails++
			systray.SetIcon(icons[stateError])
			systray.SetTooltip("Hali — service unreachable")
			if hasSeenHealthy && fails == maxFailsForRed {
				notify.Toast("Hali stopped", "Click 'Restart Service' in the tray menu to restart.")
			}
			delay := backoffError
			if fails == 1 {
				delay = backoff1
			} else if fails == 2 {
				delay = backoff2
			}
			time.Sleep(delay)
			continue
		}

		hasSeenHealthy = true
		fails = 0

		state, tooltip := queryStats()
		systray.SetIcon(icons[state])
		systray.SetTooltip(tooltip)
		time.Sleep(pollInterval)
	}
}

func checkHealth() bool {
	resp, err := http.Get(healthEndpoint) //nolint:noctx
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// statsSnapshot subset used by the tray.
type statsSubset struct {
	ActiveDLs   int `json:"active_downloads"`
	ActiveSeeds int `json:"active_seeds"`
	Models      []struct {
		ModelID string `json:"model_id"`
		Status  string `json:"status"`
	} `json:"models"`
}

func queryStats() (iconState, string) {
	resp, err := http.Get(statsEndpoint) //nolint:noctx
	if err != nil {
		return stateIdle, "Hali — idle"
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var snap statsSubset
	if err := json.Unmarshal(body, &snap); err != nil {
		return stateIdle, "Hali — idle"
	}

	if snap.ActiveDLs > 0 {
		name := ""
		for _, m := range snap.Models {
			if m.Status == "downloading" {
				name = m.ModelID
				break
			}
		}
		tip := "Hali — downloading"
		if name != "" {
			tip = "Hali — downloading " + name
		}
		return stateDownloading, tip
	}
	if snap.ActiveSeeds > 0 {
		return stateSeeding, fmt.Sprintf("Hali — seeding %d model(s)", snap.ActiveSeeds)
	}
	return stateIdle, "Hali — idle"
}

// firstRun shows a one-time welcome toast using an atomic sentinel file.
func firstRun() {
	sentinel := filepath.Join(config.DataDir(), ".first-run")
	f, err := os.OpenFile(sentinel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return // sentinel already exists; another instance won
	}
	f.Close()
	notify.Toast(
		"Hali is running in the background",
		"Models will be shared locally and cached automatically. Click 'Open Dashboard' to manage.",
	)
}

// postAPI fires a POST to the given endpoint and discards the response.
func postAPI(url string) {
	resp, err := http.Post(url, "application/json", nil) //nolint:noctx
	if err != nil {
		slog.Warn("tray POST failed", "url", url, "error", err)
		return
	}
	resp.Body.Close()
}

// openURL opens a URL in the default browser.
func openURL(url string) {
	openShell(url)
}

// openPath opens a filesystem path in Explorer/Finder.
func openPath(path string) {
	if err := os.MkdirAll(path, 0755); err != nil {
		slog.Warn("openPath: mkdir failed", "path", path)
		return
	}
	openShell(path)
}

// solidICO returns a minimal 16x16 Windows ICO filled with color c.
func solidICO(c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			img.Set(x, y, c)
		}
	}

	const width = 16
	const height = 16
	const xorStride = width * 4
	const andStride = 4
	const iconHeaderSize = 6 + 16
	const dibHeaderSize = 40
	const imageSize = dibHeaderSize + (xorStride * height) + (andStride * height)

	var buf bytes.Buffer
	writeLE := func(data any) {
		if err := binary.Write(&buf, binary.LittleEndian, data); err != nil {
			panic("solidICO: " + err.Error())
		}
	}

	writeLE(uint16(0)) // reserved
	writeLE(uint16(1)) // icon type
	writeLE(uint16(1)) // image count
	buf.WriteByte(width)
	buf.WriteByte(height)
	buf.WriteByte(0) // palette size
	buf.WriteByte(0) // reserved
	writeLE(uint16(1))
	writeLE(uint16(32))
	writeLE(uint32(imageSize))
	writeLE(uint32(iconHeaderSize))

	writeLE(uint32(dibHeaderSize))
	writeLE(int32(width))
	writeLE(int32(height * 2))
	writeLE(uint16(1))
	writeLE(uint16(32))
	writeLE(uint32(0))
	writeLE(uint32(xorStride*height + andStride*height))
	writeLE(int32(0))
	writeLE(int32(0))
	writeLE(uint32(0))
	writeLE(uint32(0))

	for y := height - 1; y >= 0; y-- {
		for x := 0; x < width; x++ {
			off := img.PixOffset(x, y)
			b := img.Pix[off+2]
			g := img.Pix[off+1]
			r := img.Pix[off+0]
			a := img.Pix[off+3]
			buf.WriteByte(b)
			buf.WriteByte(g)
			buf.WriteByte(r)
			buf.WriteByte(a)
		}
	}

	for y := 0; y < height; y++ {
		buf.Write([]byte{0, 0, 0, 0})
	}

	return buf.Bytes()
}
