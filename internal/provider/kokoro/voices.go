package kokoro

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/keshon/veyra/internal/conversation"
)

// Voice binds a public voice ID to its model-specific speaker index.
type Voice struct {
	conversation.Voice
	Speaker uint32 `json:"speaker"`
}

func LoadVoices(path string) ([]Voice, error) {
	if path == "" {
		return []Voice{
			{Voice: conversation.Voice{ID: "af_maple", Name: "Maple · American female", Language: "en", Default: true}, Speaker: 0},
			{Voice: conversation.Voice{ID: "af_sol", Name: "Sol · American female", Language: "en"}, Speaker: 1},
			{Voice: conversation.Voice{ID: "bf_vale", Name: "Vale · British female", Language: "en"}, Speaker: 2},
		}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var catalog struct {
		Voices []Voice `json:"voices"`
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err = d.Decode(&catalog); err != nil {
		return nil, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("Kokoro catalog requires one JSON object")
	}
	ids, speakers := map[string]bool{}, map[uint32]bool{}
	defaults := 0
	for _, v := range catalog.Voices {
		if v.ID == "" || v.Name == "" || v.Language != "en" || ids[v.ID] || speakers[v.Speaker] {
			return nil, fmt.Errorf("invalid or duplicate Kokoro voice %q", v.ID)
		}
		ids[v.ID], speakers[v.Speaker] = true, true
		if v.Default {
			defaults++
		}
	}
	if defaults != 1 {
		return nil, fmt.Errorf("Kokoro catalog requires one default English voice")
	}
	return catalog.Voices, nil
}

func speaker(voices []Voice, id string) (uint32, error) {
	for _, v := range voices {
		if v.ID == id || id == "" && v.Default {
			return v.Speaker, nil
		}
	}
	return 0, fmt.Errorf("unknown Kokoro voice %q", id)
}
