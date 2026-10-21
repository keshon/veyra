// Veyra starts one local service and optionally its terminal client.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/keshon/veyra/internal/config"
	"github.com/keshon/veyra/internal/service"
	"github.com/keshon/veyra/internal/terminal"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	settings, err := config.Load(os.Args[1:])
	if err != nil {
		return err
	}
	if settings.Check {
		if err = service.Check(settings); err != nil {
			return err
		}
		fmt.Printf("Installation ready: profile %s; assets present; no models started\n", settings.Profile)
		fmt.Printf("Assets: %s\nUser data: %s\n", settings.Root, settings.DataDir)
		profile, err := settings.LoadProfile()
		if err != nil {
			return err
		}
		fmt.Printf("Recognition: %s\nGeneration: %s\nSpeech: %s\nText GPU layers: %d\n", profile.Listening.Name, profile.Thinking.Name, profile.Speaking.Name, settings.GPULayers)
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if !settings.TUI {
		return service.Run(ctx, settings)
	}
	if err = service.Check(settings); err != nil {
		return err
	}
	logDir := filepath.Join(settings.DataDir, "logs")
	if err = os.MkdirAll(logDir, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(logDir, "terminal-service.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	log.SetOutput(file)
	defer log.SetOutput(os.Stderr)
	ended := make(chan error, 1)
	go func() { err := service.Run(ctx, settings); ended <- err; cancel() }()
	terminalErr := terminal.Run(ctx, "http://"+settings.Listen, terminal.Options{Root: settings.Root, DataDir: settings.DataDir, Microphone: settings.Microphone, VAD: settings.MicVAD, Live: settings.Live, Diagnostics: settings.VoiceDiagnostics, MicThreshold: settings.MicThreshold, MicStartMS: settings.MicStartMS, MicEndMS: settings.MicEndMS, MicMaxSeconds: settings.MicMaxSeconds})
	cancel()
	serviceErr := <-ended
	if serviceErr != nil {
		return serviceErr
	}
	if errors.Is(terminalErr, tea.ErrProgramKilled) {
		return nil
	}
	return terminalErr
}
