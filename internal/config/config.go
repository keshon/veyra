// Package config loads the launch settings; providers do not read flags or environment variables.
package config

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Settings struct {
	PreferCharacterPrompt      bool    `json:"prefer_character_prompt"`
	PreferCharacterPostHistory bool    `json:"prefer_character_post_history"`
	MicEndMS                   int     `json:"mic_end_ms"`
	MicMaxSeconds              int     `json:"mic_max_seconds"`
	MicVAD                     string  `json:"mic_vad"`
	VoiceDiagnostics           bool    `json:"voice_diagnostics"`
	MicThreshold               float64 `json:"mic_threshold"`
	MicStartMS                 int     `json:"mic_start_ms"`
	ThinkingModel              string  `json:"thinking_model"`
	Live                       bool    `json:"live"`
	TUI                        bool    `json:"tui"`
	Check                      bool    `json:"check"`
	Character                  string  `json:"character"`
	User                       string  `json:"user"`
	Voice                      string  `json:"voice"`
	Microphone                 string  `json:"microphone"`
	TopK                       int     `json:"top_k"`
	Seed                       int     `json:"seed"`
	Temperature                float64 `json:"temperature"`
	TopP                       float64 `json:"top_p"`
	RepeatPenalty              float64 `json:"repeat_penalty"`
	MaxTokens                  int     `json:"max_tokens"`

	Duplex       bool   `json:"duplex"`
	Prompt       string `json:"prompt"`
	Prompts      string `json:"prompts"`
	Root         string `json:"root"`
	DataDir      string `json:"data_dir"`
	Listen       string `json:"listen"`
	Profile      string `json:"profile"`
	Reply        string `json:"reply"`
	Fallback     string `json:"fallback"`
	Context      int    `json:"context"`
	GPULayers    int    `json:"gpu_layers"`
	ModelPort    int    `json:"model_port"`
	ASRPort      int    `json:"asr_port"`
	ASRCPU       bool   `json:"asr_cpu"`
	VoiceCatalog string `json:"voice_catalog"`
	ProfileFile  string `json:"profile_file"`
}

func Load(args []string) (Settings, error) {
	s := Settings{PreferCharacterPrompt: true, PreferCharacterPostHistory: true, MicEndMS: 1100, MicMaxSeconds: 120, MicVAD: "silero", MicThreshold: 0.02, MicStartMS: 300, User: "User", TopK: 40, Seed: -1, Temperature: 0.7, TopP: 0.9, RepeatPenalty: 1.05, MaxTokens: 256, Root: ".", Listen: "127.0.0.1:8090", Profile: "chatterbox-nano", Reply: "en", Fallback: "en", Context: 4096, GPULayers: 0, ModelPort: 19081, ASRPort: 19083, VoiceCatalog: "profiles/voices.json"}
	path := ""
	for i, arg := range args {
		if strings.HasPrefix(arg, "-config=") {
			path = strings.TrimPrefix(arg, "-config=")
		}
		if arg == "-config" && i+1 < len(args) {
			path = args[i+1]
		}
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return s, err
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&s); err != nil {
			return s, err
		}
		if err = decoder.Decode(new(any)); err != io.EOF {
			return s, fmt.Errorf("config must contain one JSON object")
		}
	}
	f := flag.NewFlagSet("veyra", flag.ContinueOnError)
	f.BoolVar(&s.VoiceDiagnostics, "voice-diagnostics", s.VoiceDiagnostics, "save local microphone diagnostics and enable /heard CPU transcription")
	f.BoolVar(&s.Live, "live", s.Live, "terminal hands-free listening; interruption depends on the active backend")
	f.BoolVar(&s.TUI, "tui", s.TUI, "terminal interface with typed input and push-to-talk")
	f.BoolVar(&s.Check, "check", s.Check, "check configured assets without starting models")
	f.BoolVar(&s.PreferCharacterPrompt, "character-main-prompt", s.PreferCharacterPrompt, "use character system prompt instead of the base prompt")
	f.BoolVar(&s.PreferCharacterPostHistory, "character-post-history", s.PreferCharacterPostHistory, "apply character directives after the latest user input")
	f.StringVar(&s.Character, "character", s.Character, "JSON or PNG character card path relative to root")
	f.StringVar(&s.Character, "character-png", s.Character, "PNG character card path; alias for -character")
	f.StringVar(&s.User, "user", s.User, "user name for character-card placeholders")
	f.StringVar(&s.Voice, "voice", s.Voice, "initial composed voice ID")
	f.StringVar(&s.MicVAD, "mic-vad", s.MicVAD, "speech detector: silero (CPU) or energy fallback")
	f.Float64Var(&s.MicThreshold, "mic-threshold", s.MicThreshold, "energy fallback only: minimum normalized microphone RMS (0.001 through 0.2)")
	f.IntVar(&s.MicEndMS, "mic-end-ms", s.MicEndMS, "silence before submitting speech, 400 through 5000 ms")
	f.IntVar(&s.MicMaxSeconds, "mic-max-seconds", s.MicMaxSeconds, "maximum voice recording, 5 through 120 seconds")
	f.IntVar(&s.MicStartMS, "mic-start-ms", s.MicStartMS, "energy fallback only: sustained speech before activation (100 through 600 ms)")
	f.StringVar(&s.Microphone, "microphone", s.Microphone, "DirectShow microphone name for terminal recording")
	f.IntVar(&s.TopK, "top-k", s.TopK, "top-k sampling, 0 disables")
	f.IntVar(&s.Seed, "seed", s.Seed, "sampling seed, -1 random")
	f.Float64Var(&s.Temperature, "temp", s.Temperature, "sampling temperature, 0 through 2")
	f.Float64Var(&s.TopP, "top-p", s.TopP, "nucleus sampling probability, above 0 through 1")
	f.Float64Var(&s.RepeatPenalty, "repeat-penalty", s.RepeatPenalty, "repetition penalty, 1 through 2")
	f.IntVar(&s.MaxTokens, "max-tokens", s.MaxTokens, "maximum generated tokens, 16 through 2048")
	f.BoolVar(&s.Duplex, "experimental-duplex", s.Duplex, "unvalidated native continuous streaming; installed runtime currently fails speech acceptance")
	f.StringVar(&s.Prompt, "prompt", s.Prompt, "initial system prompt ID")
	f.StringVar(&s.Prompts, "prompts", s.Prompts, "optional system prompt catalog JSON path")
	f.StringVar(&path, "config", path, "optional JSON launch configuration")
	f.StringVar(&s.Root, "root", s.Root, "project asset directory")
	f.StringVar(&s.DataDir, "data-dir", s.DataDir, "user data directory (default: .veyra in your home directory)")
	f.StringVar(&s.Listen, "listen", s.Listen, "loopback HTTP address")
	f.StringVar(&s.Profile, "profile", s.Profile, "kokoro, chatterbox, chatterbox-nano, chatterbox-turbo, composed, minicpm or minicpm-native")
	f.StringVar(&s.ThinkingModel, "thinking-model", s.ThinkingModel, "override composed thinking GGUF; relative to project root")
	f.StringVar(&s.ProfileFile, "profile-file", s.ProfileFile, "custom provider/model composition JSON manifest")
	f.StringVar(&s.Reply, "reply", s.Reply, "auto, en or ru (minicpm-native requires en)")
	f.StringVar(&s.Fallback, "fallback", s.Fallback, "reply language when detection is unavailable")
	f.IntVar(&s.Context, "ctx", s.Context, "text model context")
	f.IntVar(&s.GPULayers, "ngl", s.GPULayers, "text model GPU layers (default 0: CPU); set explicitly for your model and available VRAM")
	f.IntVar(&s.ModelPort, "model-port", s.ModelPort, "owned native model server port")
	f.IntVar(&s.ModelPort, "engine-port", s.ModelPort, "alias for model-port")
	f.IntVar(&s.ASRPort, "asr-port", s.ASRPort, "owned recognition server port")
	f.BoolVar(&s.ASRCPU, "asr-cpu", s.ASRCPU, "use CPU recognition instead of CUDA")
	f.StringVar(&s.VoiceCatalog, "voices", s.VoiceCatalog, "voice catalog JSON path (relative to root)")
	if err := f.Parse(args); err != nil {
		return s, err
	}
	if f.NArg() != 0 {
		return s, fmt.Errorf("unexpected positional arguments")
	}
	root, err := filepath.Abs(s.Root)
	if err != nil {
		return s, err
	}
	s.Root = root
	if s.DataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return s, fmt.Errorf("user data directory: %w; set -data-dir explicitly", err)
		}
		s.DataDir = filepath.Join(home, ".veyra")
	}
	s.DataDir, err = filepath.Abs(s.DataDir)
	if err != nil {
		return s, fmt.Errorf("user data directory: %w", err)
	}
	if path == "" {
		explicit := map[string]bool{}
		f.Visit(func(v *flag.Flag) { explicit[v.Name] = true })
		if err := s.loadPreferences(explicit); err != nil {
			return s, err
		}
	}
	if err := s.Generation().Validate(); err != nil {
		return s, err
	}
	if s.ProfileFile == "" && s.Profile != "composed" && s.Profile != "minicpm" && s.Profile != "minicpm-native" && s.Profile != "kokoro" && s.Profile != "chatterbox" && s.Profile != "chatterbox-nano" && s.Profile != "chatterbox-turbo" {
		return s, fmt.Errorf("unknown profile %q", s.Profile)
	}
	if s.MicVAD != "silero" && s.MicVAD != "energy" {
		return s, fmt.Errorf("mic-vad must be silero or energy")
	}
	if math.IsNaN(s.MicThreshold) || math.IsInf(s.MicThreshold, 0) || s.MicThreshold < 0.001 || s.MicThreshold > 0.2 || s.MicStartMS < 100 || s.MicStartMS > 600 {
		return s, fmt.Errorf("invalid microphone detection settings")
	}
	if s.MicEndMS < 400 || s.MicEndMS > 5000 || s.MicMaxSeconds < 5 || s.MicMaxSeconds > 120 {
		return s, fmt.Errorf("invalid microphone endpoint settings")
	}
	if s.Profile == "minicpm-native" && s.MicMaxSeconds > 29 {
		s.MicMaxSeconds = 29
	}
	if s.User == "" {
		return s, fmt.Errorf("user name cannot be empty")
	}
	if s.Duplex && s.Profile != "minicpm-native" {
		return s, fmt.Errorf("duplex requires minicpm-native")
	}
	host, port, err := net.SplitHostPort(s.Listen)
	ip := net.ParseIP(host)
	n, _ := strconv.Atoi(port)
	if err != nil || !(host == "localhost" || ip != nil && ip.IsLoopback()) || n < 1 || n > 65535 {
		return s, fmt.Errorf("listen must be a loopback address with a valid port")
	}
	if s.GPULayers < 0 || s.ModelPort < 1 || s.ModelPort > 65535 || s.ASRPort < 1 || s.ASRPort > 65535 {
		return s, fmt.Errorf("invalid context, GPU layers or native port")
	}
	if s.ModelPort == s.ASRPort && s.Profile != "minicpm-native" || s.ModelPort == n || s.Profile != "minicpm-native" && s.ASRPort == n {
		return s, fmt.Errorf("ports must be distinct")
	}
	return s, nil
}
