package av1

import (
	"encoding/hex"
	"strings"

	"github.com/AlexxIT/go2rtc/pkg/bits"
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

// WidthHeight extracts max_frame_width/height from the Sequence Header OBU
// embedded in an av1C AV1CodecConfigurationRecord (4-byte header followed by
// configOBUs). Returns (0, 0) when no sequence header is present or parsing
// fails. Needed because Safari sizes <video> from the mp4 sample-entry dims
// and ignores the in-band sequence header (Chrome does the opposite).
func WidthHeight(conf []byte) (width, height uint16) {
	if len(conf) < 5 {
		return 0, 0
	}
	obus := conf[4:] // skip av1C fixed header

	for len(obus) > 0 {
		b := obus[0]
		obuType := (b >> 3) & 0x0F
		i := 1
		if b&0x04 != 0 { // obu_extension_flag
			i++
		}
		size := len(obus) - i
		if b&0x02 != 0 { // obu_has_size_field
			v, n := leb128(obus[i:])
			if n == 0 {
				return 0, 0
			}
			i += n
			size = int(v)
		}
		if size < 0 || i+size > len(obus) {
			return 0, 0
		}
		if obuType == OBUSequenceHeader {
			return parseSequenceHeader(obus[i : i+size])
		}
		obus = obus[i+size:]
	}
	return 0, 0
}

// parseSequenceHeader walks sequence_header_obu (AV1 spec 5.5.1) far enough
// to reach max_frame_width_minus_1 / max_frame_height_minus_1.
func parseSequenceHeader(b []byte) (uint16, uint16) {
	r := bits.NewReader(b)

	_ = r.ReadBits8(3) // seq_profile
	_ = r.ReadBit()    // still_picture

	if r.ReadBit() != 0 { // reduced_still_picture_header
		_ = r.ReadBits8(5) // seq_level_idx[0]
	} else {
		decoderModelInfo := false
		var bufferDelayLen byte

		if r.ReadBit() != 0 { // timing_info_present_flag
			_ = r.ReadBits(32)    // num_units_in_display_tick
			_ = r.ReadBits(32)    // time_scale
			if r.ReadBit() != 0 { // equal_picture_interval
				_ = r.ReadUEGolomb() // num_ticks_per_picture_minus_1, uvlc()
			}
			decoderModelInfo = r.ReadBit() != 0 // decoder_model_info_present_flag
			if decoderModelInfo {
				bufferDelayLen = r.ReadBits8(5) + 1 // buffer_delay_length_minus_1
				_ = r.ReadBits(32)                  // num_units_in_decoding_tick
				_ = r.ReadBits8(5)                  // buffer_removal_time_length_minus_1
				_ = r.ReadBits8(5)                  // frame_presentation_time_length_minus_1
			}
		}

		initialDisplayDelay := r.ReadBit() != 0 // initial_display_delay_present_flag
		opCnt := int(r.ReadBits8(5)) + 1        // operating_points_cnt_minus_1

		for i := 0; i < opCnt; i++ {
			_ = r.ReadBits16(12)        // operating_point_idc[i]
			if r.ReadBits8(5) > 7 {     // seq_level_idx[i]
				_ = r.ReadBit()         // seq_tier[i]
			}
			if decoderModelInfo {
				if r.ReadBit() != 0 { // decoder_model_present_for_this_op[i]
					_ = r.ReadBits64(bufferDelayLen) // decoder_buffer_delay
					_ = r.ReadBits64(bufferDelayLen) // encoder_buffer_delay
					_ = r.ReadBit()                  // low_delay_mode_flag
				}
			}
			if initialDisplayDelay {
				if r.ReadBit() != 0 { // initial_display_delay_present_for_this_op[i]
					_ = r.ReadBits8(4) // initial_display_delay_minus_1[i]
				}
			}
		}
	}

	widthBits := r.ReadBits8(4) + 1  // frame_width_bits_minus_1
	heightBits := r.ReadBits8(4) + 1 // frame_height_bits_minus_1
	w := r.ReadBits(widthBits) + 1   // max_frame_width_minus_1
	h := r.ReadBits(heightBits) + 1  // max_frame_height_minus_1

	if r.EOF || w > 0xFFFF || h > 0xFFFF {
		return 0, 0
	}
	return uint16(w), uint16(h)
}

// leb128 decodes an unsigned LEB128 value (AV1 spec 4.10.5). Returns the
// number of bytes consumed, or 0 on malformed input.
func leb128(b []byte) (v uint64, n int) {
	for i := 0; i < len(b) && i < 8; i++ {
		v |= uint64(b[i]&0x7F) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return 0, 0
}

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
