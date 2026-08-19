package ffmpeg

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"
)

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found on PATH — skipping (mirrors dbtest.Open's skip-if-unreachable pattern)")
	}
}

// syntheticOgg generates a tiny silent .ogg fixture with ffmpeg itself,
// rather than committing a binary fixture (docs/BRIEF-ai-intent.md gives
// explicit latitude to do this when a real audio fixture isn't
// practical to commit).
func syntheticOgg(t *testing.T) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono",
		"-t", "0.2",
		"-c:a", "libopus",
		"-f", "ogg", "pipe:1",
	)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("generating synthetic ogg fixture: %v: %s", err, stderr.String())
	}
	if out.Len() == 0 {
		t.Fatalf("generating synthetic ogg fixture produced no output: %s", stderr.String())
	}
	return out.Bytes()
}

func TestTranscode_OggToMP3(t *testing.T) {
	requireFFmpeg(t)

	ogg := syntheticOgg(t)

	tr := New()
	mp3, err := tr.Transcode(context.Background(), ogg)
	if err != nil {
		t.Fatalf("Transcode: %v", err)
	}
	if len(mp3) == 0 {
		t.Fatal("got empty mp3 output")
	}
	// ID3/MP3 frame sync or tag markers — enough to prove this isn't
	// just the ogg bytes echoed back unchanged.
	if bytes.Equal(mp3, ogg) {
		t.Fatal("got the same bytes back unchanged, want an actual mp3 transcode")
	}
}

func TestTranscode_InvalidInput(t *testing.T) {
	requireFFmpeg(t)

	tr := New()
	if _, err := tr.Transcode(context.Background(), []byte("not audio at all")); err == nil {
		t.Fatal("expected an error for non-audio input, got nil")
	}
}
