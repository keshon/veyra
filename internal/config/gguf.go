package config

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

// CheckChatModel checks GGUF metadata, not its filename or tensor weights. Audio
// encoders, projectors, TTS decoders and cache files are not chat models.
func CheckChatModel(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReader(io.LimitReader(f, 64<<20))
	var magic [4]byte
	var version uint32
	var tensors, fields uint64
	if _, err = io.ReadFull(r, magic[:]); err != nil {
		return err
	}
	read := func(v any) error { return binary.Read(r, binary.LittleEndian, v) }
	if string(magic[:]) != "GGUF" {
		return fmt.Errorf("not a GGUF model")
	}
	if err = read(&version); err != nil {
		return err
	}
	if version != 2 && version != 3 {
		return fmt.Errorf("unsupported GGUF version")
	}
	if err = read(&tensors); err != nil {
		return err
	}
	if err = read(&fields); err != nil {
		return err
	}
	if tensors == 0 || fields > 65536 {
		return fmt.Errorf("invalid GGUF model header")
	}
	text := func() (string, error) {
		var n uint64
		if err := read(&n); err != nil {
			return "", err
		}
		if n > 1<<20 {
			return "", fmt.Errorf("GGUF string exceeds limit")
		}
		b := make([]byte, n)
		_, err := io.ReadFull(r, b)
		return string(b), err
	}
	sizes := map[uint32]uint64{0: 1, 1: 1, 2: 2, 3: 2, 4: 4, 5: 4, 6: 4, 7: 1, 10: 8, 11: 8, 12: 8}
	var skip func(uint32) error
	skip = func(t uint32) error {
		if n, ok := sizes[t]; ok {
			_, err := io.CopyN(io.Discard, r, int64(n))
			return err
		}
		if t == 8 {
			var n uint64
			if err := read(&n); err != nil {
				return err
			}
			if n > 64<<20 {
				return fmt.Errorf("GGUF value exceeds limit")
			}
			_, err := io.CopyN(io.Discard, r, int64(n))
			return err
		}
		if t == 9 {
			var kind uint32
			var count uint64
			if err := read(&kind); err != nil {
				return err
			}
			if err := read(&count); err != nil {
				return err
			}
			if count > 1<<20 || kind == 9 {
				return fmt.Errorf("invalid GGUF array")
			}
			if size, ok := sizes[kind]; ok {
				_, err := io.CopyN(io.Discard, r, int64(count*size))
				return err
			}
			for i := uint64(0); i < count; i++ {
				if err := skip(kind); err != nil {
					return err
				}
			}
			return nil
		}
		return fmt.Errorf("unsupported GGUF value type")
	}
	arch, name, kind := "", "", ""
	tokenizer, chat := false, false
	for i := uint64(0); i < fields; i++ {
		key, err := text()
		if err != nil {
			return err
		}
		var t uint32
		if err = read(&t); err != nil {
			return err
		}
		if t == 8 && (key == "general.architecture" || key == "general.name" || key == "general.type" || key == "tokenizer.ggml.model" || key == "tokenizer.chat_template") {
			value, err := text()
			if err != nil {
				return err
			}
			switch key {
			case "general.architecture":
				arch = value
			case "general.name":
				name = value
			case "general.type":
				kind = value
			case "tokenizer.ggml.model":
				tokenizer = value != ""
			case "tokenizer.chat_template":
				chat = value != ""
			}
		} else if err = skip(t); err != nil {
			return err
		}
	}
	lower := strings.ToLower(name)
	if arch == "" || !tokenizer || !chat || kind != "" && kind != "model" || lower == "tts" || strings.Contains(lower, "text-to-speech") {
		return fmt.Errorf("not a text chat model (audio, projection and speech components cannot generate conversation)")
	}
	switch arch {
	case "whisper", "clip", "projector", "hifigan2", "wavtokenizer", "flow_matching", "flow_extra", "upsample_encoder_v2":
		return fmt.Errorf("%s is a voice component, not a text chat model", arch)
	}
	return nil
}
