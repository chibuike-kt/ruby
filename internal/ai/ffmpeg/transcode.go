// Package ffmpeg implements internal/ai's Transcoder interface by
// shelling out to the ffmpeg binary — the format-mismatch fix
// docs/BRIEF-ai-intent.md calls out explicitly: WhatsApp voice notes
// arrive as .ogg/Opus, which gpt-transcribe does not accept.
package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// Transcoder shells out to ffmpeg on PATH. It holds no state — New only
// exists so ai.Config can wire it in the same shape as every other
// collaborator.
type Transcoder struct{}

func New() *Transcoder {
	return &Transcoder{}
}

// Transcode converts ogg (Opus) bytes to mp3 via stdin/stdout pipes —
// no temp files, nothing left on disk for a voice note that may contain
// financial details.
func (t *Transcoder) Transcode(ctx context.Context, ogg []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-i", "pipe:0",
		"-f", "mp3",
		"pipe:1",
	)
	cmd.Stdin = bytes.NewReader(ogg)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: transcode failed: %w: %s", err, stderr.String())
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("ffmpeg: transcode produced no output: %s", stderr.String())
	}
	return stdout.Bytes(), nil
}
