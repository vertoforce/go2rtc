package mp4

import (
	"io"
	"testing"
	"time"

	"github.com/AlexxIT/go2rtc/pkg/core"
)

const testH264Fmtp = "packetization-mode=1;profile-level-id=64001f;sprop-parameter-sets=Z2QAH6wkhAFAFuwEQAAAAwBAAAAMI8YMkg==,aO4yyLA="

// writeToAfterStop starts a consumer whose track never delivers a keyframe,
// then stops it. WriteTo has to return, or every request for a stream that is
// down leaks a goroutine.
func writeToAfterStop(t *testing.T, codec *core.Codec) {
	cons := NewConsumer(nil)
	media := cons.Medias[0]
	track := core.NewReceiver(media, codec)
	if err := cons.AddTrack(media, codec, track); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		_, _ = cons.WriteTo(io.Discard)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	_ = cons.Stop()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s: WriteTo still blocked 2s after Stop", codec.Name)
	}
}

func TestConsumerStopBeforeKeyframeH264(t *testing.T) {
	writeToAfterStop(t, &core.Codec{Name: core.CodecH264, ClockRate: 90000, PayloadType: core.PayloadTypeRAW, FmtpLine: testH264Fmtp})
}

func TestConsumerStopBeforeKeyframeAV1(t *testing.T) {
	writeToAfterStop(t, &core.Codec{Name: core.CodecAV1, ClockRate: 90000, PayloadType: core.PayloadTypeRAW})
}
