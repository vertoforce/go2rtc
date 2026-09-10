package av1

import (
	"encoding/hex"
	"strings"

	"github.com/AlexxIT/go2rtc/pkg/core"
)

// ConfigToCodec parses an AV1CodecConfigurationRecord (av1C blob, as carried
// in Enhanced-FLV PacketTypeSequenceStart and ISOBMFF mp4 files) into a
// core.Codec. The full conf blob is stored hex-encoded in FmtpLine so a
// downstream mp4 muxer can write it back into the av1C sample-entry box.
//
// av1C layout (AV1-ISOBMFF):
//
//	byte 0 : marker(1)=1 | version(7)=1   → 0x81
//	byte 1 : seq_profile(3) | seq_level_idx_0(5)
//	byte 2 : seq_tier_0(1) | high_bit_depth(1) | twelve_bit(1) | monochrome(1)
//	         | chroma_subsampling_x(1) | y(1) | sample_position(2)
//	byte 3 : reserved(3) | initial_presentation_delay_present(1) | delay(4)
//	bytes 4..N : configOBUs (the AV1 sequence_header OBU)
func ConfigToCodec(conf []byte) *core.Codec {
	c := &core.Codec{
		Name:        core.CodecAV1,
		ClockRate:   90000,
		PayloadType: core.PayloadTypeRAW,
	}
	if len(conf) > 0 {
		c.FmtpLine = "av1c=" + hex.EncodeToString(conf)
	}
	return c
}

// GetConfig retrieves the av1C blob previously stored by ConfigToCodec.
// Returns nil if FmtpLine doesn't carry one.
func GetConfig(fmtp string) []byte {
	const tag = "av1c="
	i := strings.Index(fmtp, tag)
	if i < 0 {
		return nil
	}
	s := fmtp[i+len(tag):]
	if j := strings.IndexByte(s, ';'); j >= 0 {
		s = s[:j]
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

// AV1 OBU types (AV1 spec 5.3.2)
const (
	OBUSequenceHeader = 1
	OBUTemporalDelim  = 2
	OBUFrameHeader    = 3
	OBUTileGroup      = 4
	OBUMetadata       = 5
	OBUFrame          = 6
	OBURedundantFH    = 7
	OBUTileList       = 8
)

// IsKeyframe scans a Low-Overhead-Bitstream-Format (LOB) AV1 frame and
// returns true if a SEQUENCE_HEADER OBU appears at the front. Encoders
// prefix every keyframe with a fresh sequence header. ffmpeg's flv muxer
// (and many others) emits a leading TEMPORAL_DELIMITER OBU before the
// sequence header on every access unit, so we transparently skip a
// single TD before looking. This is cheap without parsing the full
// frame_header_obu syntax.
func IsKeyframe(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	// First OBU header byte: bits[6..3] = obu_type, bit[1] = has_size.
	obuType := (data[0] >> 3) & 0x0F
	if obuType == OBUTemporalDelim {
		// TD OBU is always size 0 with has_size=1, so the OBU is 2 bytes
		// total: header byte + LEB128 size byte (0x00). Skip it and
		// re-check the next OBU.
		if len(data) < 2 {
			return false
		}
		data = data[2:]
		if len(data) == 0 {
			return false
		}
		obuType = (data[0] >> 3) & 0x0F
	}
	return obuType == OBUSequenceHeader
}
