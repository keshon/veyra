package conversation

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/keshon/veyra/internal/language"
)

type cloningFake struct {
	fake
	samples int
}

func (f *cloningFake) Capabilities() Capabilities {
	c := f.fake.Capabilities()
	c.ReferenceConditioning = true
	return c
}
func (f *cloningFake) SetReference(context.Context, []byte) error { f.samples++; return nil }

func TestReferenceValidationAdmissionAndHistory(t *testing.T) {
	f := &cloningFake{}
	s, err := New(f, language.Preferences{Reply: "en", Fallback: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = s.SetReference(context.Background(), []byte("invalid")); err == nil || f.samples != 0 {
		t.Fatal("invalid sample reached provider")
	}
	b := make([]byte, 44+96000)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], 96000)
	s.busy = true
	if err = s.SetReference(context.Background(), b); err != ErrBusy {
		t.Fatal(err)
	}
	s.busy = false
	s.history = []Message{{Role: "user", Text: "old"}}
	if err = s.SetReference(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if len(s.history) != 0 || s.Status().Reference != "Custom reference" || f.samples != 1 {
		t.Fatal(s.Status())
	}
	if err = s.Reset(context.Background()); err != nil || s.Status().Reference != "Custom reference" {
		t.Fatal(err, s.Status())
	}
	if err = s.SetReference(context.Background(), nil); err != nil || s.Status().Reference != "Original reference" {
		t.Fatal(err, s.Status())
	}
}
