package conversation

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/keshon/veyra/internal/language"
)

// StorePrompts enables local prompt preference persistence. Configure before
// admitting operations; loading and validation belong to application startup.
func (s *Session) StorePrompts(path string) { s.promptPath = path }
func (s *Session) storePrompt(p language.Preferences) error {
	if s.promptPath == "" {
		return nil
	}
	data, err := json.Marshal(struct {
		ID     string `json:"prompt"`
		Custom string `json:"custom_prompt,omitempty"`
	}{p.PromptID, p.CustomPrompt})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.promptPath), ".prompt-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.promptPath)
}
