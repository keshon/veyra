// Package service composes local providers and owns application startup and shutdown.
package service

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/keshon/veyra/internal/character"
	"github.com/keshon/veyra/internal/config"
	"github.com/keshon/veyra/internal/conversation"
	"github.com/keshon/veyra/internal/httpapi"
	"github.com/keshon/veyra/internal/language"
	"github.com/keshon/veyra/internal/process"
	"github.com/keshon/veyra/internal/provider/chatterbox"
	"github.com/keshon/veyra/internal/provider/kokoro"
	"github.com/keshon/veyra/internal/provider/llamacpp"
	"github.com/keshon/veyra/internal/provider/minicpm"
	"github.com/keshon/veyra/internal/provider/piper"
	"github.com/keshon/veyra/internal/provider/whisper"
	"github.com/keshon/veyra/internal/speech"
)

// serveRuntime owns one set of model processes until its context is canceled.
func serveRuntime(ctx context.Context, settings config.Settings, report func(string, string), publish func(*runtime)) error {
	if err := checkModelSetup(settings); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	profile, err := settings.LoadProfile()
	if err != nil {
		return err
	}
	root := settings.Root
	asset := func(p string) string { return filepath.Join(root, p) }
	if settings.DataDir == "" {
		return fmt.Errorf("user data directory is required")
	}
	log.Printf("Starting %s", profile.Name)
	sessionsDir := filepath.Join(settings.DataDir, "sessions")
	if err = os.MkdirAll(sessionsDir, 0700); err != nil {
		return err
	}
	out, err := os.MkdirTemp(sessionsDir, "session-")
	if err != nil {
		return err
	}
	phase := func(stage string) {
		log.Print(stage)
		if report != nil {
			report(stage, out)
		}
	}
	phase("Checking installed files")
	audioDir := filepath.Join(out, "audio")
	nativeDir := filepath.Join(out, "native")
	for _, dir := range []string{audioDir, nativeDir} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	address := fmt.Sprintf("http://127.0.0.1:%d", settings.ModelPort)
	model := profile.Thinking.Model
	args := []string{"--host", "127.0.0.1", "--port", strconv.Itoa(settings.ModelPort), "-m", model, "-c", strconv.Itoa(settings.Context), "-ngl", strconv.Itoa(settings.GPULayers), "-t", "2", "-b", "128", "--parallel", "1", "--jinja", "--reasoning-budget", "0"}
	type launch struct {
		name, binary string
		args         []string
		port         int
	}
	launches := []launch{}
	var backend conversation.Backend
	var apply func(config.Settings)
	warm := func(context.Context) error { return nil }
	prompts, err := config.LoadPrompts(root, settings.Prompts)
	if err != nil {
		return err
	}
	metadata, err := Metadata(settings)
	if err != nil {
		return err
	}
	for _, warning := range metadata.Warnings {
		log.Printf("Character import: %s", warning)
	}
	if settings.Character != "" {
		card, e := character.Load(resolve(root, settings.Character))
		if e != nil {
			return e
		}
		system, post := card.Compile(settings.User, prompts[0].Instruction, settings.PreferCharacterPrompt, settings.PreferCharacterPostHistory, prompts[0].PostHistory)
		prompts = append(prompts, conversation.Prompt{ID: "character", Name: card.Name, Instruction: system, PostHistory: post})
		settings.Prompt = "character"
	}
	prefs := language.Preferences{PromptID: settings.Prompt, Reply: settings.Reply, Fallback: settings.Fallback}
	if settings.Voice != "" {
		prefs.Voices = map[string]string{"en": settings.Voice}
	}
	if profile.Mode == "minicpm-native" {
		interruptionMode := "drain"
		if profile.Native.SettledCancellation {
			if settings.Duplex {
				return fmt.Errorf("settled native cancellation supports simplex sessions only")
			}
			interruptionMode = "cancel"
		}
		args = []string{"--host", "127.0.0.1", "--port", strconv.Itoa(settings.ModelPort), "--model", model, "--ctx-size", strconv.Itoa(settings.Context), "--n-gpu-layers", strconv.Itoa(settings.GPULayers), "--parallel", "1", "--batch-size", "256", "--ubatch-size", "128", "--predict", strconv.Itoa(settings.MaxTokens), "--threads", "6", "--top-k", strconv.Itoa(settings.TopK), "--seed", strconv.Itoa(settings.Seed), "--temp", strconv.FormatFloat(settings.Temperature, 'f', -1, 64), "--top-p", strconv.FormatFloat(settings.TopP, 'f', -1, 64), "--repeat-penalty", strconv.FormatFloat(settings.RepeatPenalty, 'f', -1, 64)}
		backend = minicpm.NewConfigured(nativeDir, address, &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Minute}, minicpm.Config{Duplex: settings.Duplex, SettledCancellation: profile.Native.SettledCancellation, ModelDir: profile.Native.ModelDir, TTSDir: profile.Native.TTSDir, Reference: profile.Native.Reference, Capabilities: conversation.Capabilities{Interruptions: true, InterruptionMode: interruptionMode, Profile: profile.Name, Languages: []string{"en"}, Listening: profile.Listening.Name, Thinking: profile.Thinking.Name, Speaking: profile.Speaking.Name, ReferenceConditioning: true}})
	} else {
		asrBinary := profile.Listening.Binary
		if settings.ASRCPU {
			asrBinary = profile.Listening.CPUBinary
			if asrBinary == "" {
				return fmt.Errorf("profile has no CPU recognition runtime")
			}
		}
		asrModel := profile.Listening.Model
		launches = append(launches, launch{"whisper", asrBinary, []string{"-m", asrModel, "--host", "127.0.0.1", "--port", strconv.Itoa(settings.ASRPort), "-t", "4", "-l", "auto", "-bs", "5", "-bo", "5", "-nt", "-nc", "-nlp"}, settings.ASRPort})

		generator := llamacpp.NewConfigured(address, llamacpp.Sampling{TopK: settings.TopK, Seed: settings.Seed, Temperature: settings.Temperature, TopP: settings.TopP, RepeatPenalty: settings.RepeatPenalty, MaxTokens: settings.MaxTokens, ContextTokens: settings.Context})
		apply = func(s config.Settings) {
			generator.SetSampling(llamacpp.Sampling{Temperature: s.Temperature, TopP: s.TopP, TopK: s.TopK, Seed: s.Seed, RepeatPenalty: s.RepeatPenalty, MaxTokens: s.MaxTokens, ContextTokens: s.Context})
		}
		caps := conversation.Capabilities{Profile: profile.Name, Languages: []string{"en", "ru"}, AutomaticLanguage: true, TextInput: true, Listening: profile.Listening.Name, Thinking: profile.Thinking.Name, Speaking: profile.Speaking.Name}
		var synth speech.Synthesizer
		recognizer := whisper.New(fmt.Sprintf("http://127.0.0.1:%d", settings.ASRPort))
		residentSpeech := profile.Speaking.Provider == "kokoro" || profile.Speaking.Provider == "chatterbox"
		if residentSpeech {
			caps.Languages = []string{"en"}
			caps.AutomaticLanguage = false
			recognizer = whisper.NewEnglish(fmt.Sprintf("http://127.0.0.1:%d", settings.ASRPort))
			launches[0].args = []string{"-m", asrModel, "--host", "127.0.0.1", "--port", strconv.Itoa(settings.ASRPort), "-t", "2", "-l", "en", "-bs", "1", "-bo", "1", "-nt", "-nc", "-nlp"}
		}
		if profile.Speaking.Provider == "kokoro" {
			workerLog, e := os.Create(filepath.Join(out, "kokoro.log"))
			if e != nil {
				return e
			}
			defer workerLog.Close()
			voices, e := kokoro.LoadVoices(profile.Speaking.Voices)
			if e != nil {
				return e
			}
			worker := kokoro.New(ctx, profile.Speaking.Binary, profile.Speaking.Library, profile.Speaking.Model, voices, workerLog)
			defer worker.Close()
			synth = worker
		} else if profile.Speaking.Provider == "chatterbox" {
			workerLog, e := os.Create(filepath.Join(out, "chatterbox.log"))
			if e != nil {
				return e
			}
			defer workerLog.Close()
			worker, e := chatterbox.NewVariant(ctx, profile.Speaking.Binary, profile.Speaking.Model, nativeDir, profile.Speaking.Variant, workerLog)
			if e != nil {
				return e
			}
			defer worker.Close()
			synth = worker
		} else {
			catalogPath := profile.Speaking.Voices
			if settings.VoiceCatalog != "profiles/voices.json" {
				catalogPath = asset(settings.VoiceCatalog)
			}
			catalog, e := config.LoadVoices(root, catalogPath)
			if e != nil {
				return e
			}
			voices := make([]piper.Voice, 0, len(catalog))
			for _, v := range catalog {
				voices = append(voices, piper.Voice{Voice: conversation.Voice{ID: v.ID, Name: v.Name, Language: v.Language, Default: v.Default}, Model: v.Model, Speaker: v.Speaker})
			}
			synth = piper.New(profile.Speaking.Binary, nativeDir, voices)
		}
		if residentSpeech {
			warm = func(ctx context.Context) error {
				phase("Preparing speech recognition")
				if _, err := recognizer.Recognize(ctx, silenceWAV()); err != nil && !errors.Is(err, whisper.ErrNoSpeech) {
					return err
				}
				phase("Preparing the conversation model")
				warmGenerator := llamacpp.NewConfigured(address, llamacpp.Sampling{Temperature: 0, TopP: 1, RepeatPenalty: 1, MaxTokens: 8, ContextTokens: settings.Context})
				if err := warmGenerator.Generate(ctx, []conversation.Message{{Role: "system", Text: "Reply with Ready."}, {Role: "user", Text: "Hello."}}, func(string) error { return nil }); err != nil && !errors.Is(err, conversation.ErrResponseLimit) {
					return err
				}
				phase("Loading " + strings.Split(profile.Speaking.Name, " · ")[0])
				speechCtx, cancelSpeech := context.WithTimeout(ctx, 90*time.Second)
				defer cancelSpeech()
				_, err := synth.Synthesize(speechCtx, "Ready.", "en", settings.Voice)
				return err
			}
		}
		backend = speech.NewConfigured(recognizer, generator, synth, caps)
	}
	launches = append(launches, launch{"llamacpp", profile.Thinking.Binary, args, settings.ModelPort})
	if _, err = os.Stat(model); err != nil {
		return err
	}
	promptPath := filepath.Join(settings.DataDir, "prompt.json")
	if data, e := os.ReadFile(promptPath); e == nil {
		var saved struct {
			ID     string `json:"prompt"`
			Custom string `json:"custom_prompt"`
		}
		if e = json.Unmarshal(data, &saved); e != nil {
			return fmt.Errorf("saved prompt: %w", e)
		}
		if len(saved.Custom) > 4096 {
			return fmt.Errorf("saved custom prompt exceeds 4096 bytes")
		}
		// Explicit launch prompt/card takes precedence over the saved selection.
		if settings.Prompt == "" {
			prefs.PromptID, prefs.CustomPrompt = saved.ID, saved.Custom
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	session, err := conversation.NewWithPrompts(backend, prefs, prompts)
	if err != nil {
		return err
	}
	session.StorePrompts(promptPath)
	if err = session.EnableArchive(filepath.Join(settings.DataDir, "conversations")); err != nil {
		return fmt.Errorf("conversation storage: %w", err)
	}
	// Check all native ports before starting any child; never attach to an existing process.
	for _, spec := range launches {
		if _, err = os.Stat(spec.binary); err != nil {
			return err
		}
		probe, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", spec.port))
		if e != nil {
			return fmt.Errorf("native port %d is occupied by another application; stop its owning application before retrying (no model processes were started): %w", spec.port, e)
		}
		probe.Close()
	}
	var children []*process.Child
	var logs []*os.File
	defer func() {
		session.Cancel()
		cancel()
		for i := len(children) - 1; i >= 0; i-- {
			children[i].Stop()
		}
		for _, file := range logs {
			file.Close()
		}
	}()
	for _, spec := range launches {
		file, e := os.Create(filepath.Join(out, spec.name+".log"))
		if e != nil {
			return e
		}
		logs = append(logs, file)
		child, e := process.Start(ctx, spec.binary, root, spec.args, file)
		if e != nil {
			return e
		}
		children = append(children, child)
		go func(name string, c *process.Child) {
			e := c.Wait()
			if ctx.Err() == nil {
				session.Fail(fmt.Errorf("%s exited: %v; restart the application", name, e))
			}
		}(spec.name, child)
	}
	ready, stop := context.WithTimeout(ctx, 2*time.Minute)
	defer stop()
	for i, c := range children {
		phase(map[string]string{"whisper": "Loading speech recognition", "llamacpp": "Loading the conversation model"}[launches[i].name])
		if err := c.Ready(ready, fmt.Sprintf("http://127.0.0.1:%d", launches[i].port)); err != nil {
			return fmt.Errorf("%s failed; see %s: %w", launches[i].name, filepath.Join(out, launches[i].name+".log"), err)
		}
	}
	if err := warm(ready); err != nil {
		return fmt.Errorf("warmup: %w", err)
	}
	phase("Preparing the voice session")
	if err := session.Initialize(ready); err != nil {
		return err
	}
	phase("Ready")
	publish(&runtime{session: session, apply: apply, handler: httpapi.HandlerConfigured(session, audioDir, settings.Context, metadata)})
	<-ctx.Done()
	return nil
}

func silenceWAV() []byte {
	b := make([]byte, 44+6400)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint32(b[28:], 32000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 6400)
	return b
}
