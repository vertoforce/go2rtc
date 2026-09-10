package av1

import (
	"fmt"

	"github.com/AlexxIT/go2rtc/pkg/core"
)

// SequenceHeaderInfo holds parsed fields from an AV1 Sequence Header OBU.
// Used for av1C box generation, MIME codec strings, and resolution detection.
type SequenceHeaderInfo struct {
	Profile    byte
	Level      byte // seq_level_idx_0
	Tier       byte // 0=Main, 1=High
	BitDepth   byte // 8, 10, or 12
	Monochrome bool
	Width      uint16
	Height     uint16

	// Chroma subsampling (for av1C box)
	ChromaSubsamplingX byte
	ChromaSubsamplingY byte
	ChromaSamplePos    byte

	// Raw flags for av1C encoding
	highBitdepth byte
	twelveBit    byte
}

// ParseSequenceHeaderInfo parses a raw Sequence Header OBU (with OBU header + size)
// and returns all relevant fields. Returns nil if the OBU is too short or invalid.
func ParseSequenceHeaderInfo(obu []byte) *SequenceHeaderInfo {
	if len(obu) < 2 {
		return nil
	}

	hdrSize := OBUHeaderSize(obu[0])
	data := obu[hdrSize:]

	if OBUHasSize(obu[0]) {
		_, sizeLen := ReadLEB128(data)
		data = data[sizeLen:]
	}

	if len(data) < 3 {
		return nil
	}

	info := &SequenceHeaderInfo{
		ChromaSubsamplingX: 1,
		ChromaSubsamplingY: 1,
	}

	br := &bitReader{data: data}

	// seq_profile (3 bits)
	info.Profile = byte(br.readBits(3))

	// still_picture (1 bit)
	br.readBits(1)

	// reduced_still_picture_header (1 bit)
	reducedStill := br.readBits(1)

	if reducedStill == 1 {
		info.Level = byte(br.readBits(5))
		info.Tier = 0
		// reduced_still has no frame dimensions in the normal path;
		// leave width/height = 0 (caller should use defaults)
		info.BitDepth = 8
		if br.eof {
			return nil
		}
		return info
	}

	// timing_info_present_flag
	var decoderModelInfo bool
	var bufferDelayLength uint32

	if br.readBits(1) == 1 {
		br.readBits(32)          // num_units_in_display_tick
		br.readBits(32)          // time_scale
		if br.readBits(1) == 1 { // equal_picture_interval
			br.readUVLC() // num_ticks_per_picture_minus_1
		}
		if br.readBits(1) == 1 { // decoder_model_info_present_flag
			decoderModelInfo = true
			bufferDelayLength = br.readBits(5) + 1 // buffer_delay_length_minus_1
			br.readBits(32)                        // num_units_in_decoding_tick
			br.readBits(5)                         // buffer_removal_time_length_minus_1
			br.readBits(5)                         // frame_presentation_time_length_minus_1
		}
	}

	initialDisplayDelay := br.readBits(1) == 1 // initial_display_delay_present_flag

	// operating points
	opPoints := br.readBits(5) + 1
	for i := uint32(0); i < opPoints; i++ {
		br.readBits(12) // operating_point_idc
		lvl := br.readBits(5)
		if i == 0 {
			info.Level = byte(lvl)
		}
		if lvl > 7 {
			t := br.readBits(1)
			if i == 0 {
				info.Tier = byte(t)
			}
		}
		if decoderModelInfo && br.readBits(1) == 1 { // decoder_model_present_for_this_op
			br.readBits(bufferDelayLength) // decoder_buffer_delay
			br.readBits(bufferDelayLength) // encoder_buffer_delay
			br.readBits(1)                 // low_delay_mode_flag
		}
		if initialDisplayDelay && br.readBits(1) == 1 { // initial_display_delay_present_for_this_op
			br.readBits(4) // initial_display_delay_minus_1
		}
	}

	// frame dimensions
	frameWidthBits := br.readBits(4) + 1
	frameHeightBits := br.readBits(4) + 1
	info.Width = uint16(br.readBits(frameWidthBits) + 1)
	info.Height = uint16(br.readBits(frameHeightBits) + 1)

	// frame_id_numbers_present_flag
	if br.readBits(1) == 1 {
		br.readBits(4) // delta_frame_id_length_minus_2
		br.readBits(3) // additional_frame_id_length_minus_1
	}

	br.readBits(1) // use_128x128_superblock
	br.readBits(1) // enable_filter_intra
	br.readBits(1) // enable_intra_edge_filter

	// inter-frame features (not present in reduced_still)
	br.readBits(1) // enable_interintra_compound
	br.readBits(1) // enable_masked_compound
	br.readBits(1) // enable_warped_motion
	br.readBits(1) // enable_dual_filter

	enableOrderHint := br.readBits(1)
	if enableOrderHint == 1 {
		br.readBits(1) // enable_jnt_comp
		br.readBits(1) // enable_ref_frame_mvs
	}

	// screen content tools (AV1 spec Section 5.5.2)
	seqChooseSCT := br.readBits(1)
	seqForceSCT := uint32(2) // SELECT_SCREEN_CONTENT_TOOLS
	if seqChooseSCT == 0 {
		seqForceSCT = br.readBits(1) // seq_force_screen_content_tools
	}
	if seqForceSCT > 0 {
		seqChooseIntMV := br.readBits(1) // seq_choose_integer_mv
		if seqChooseIntMV == 0 {
			br.readBits(1) // seq_force_integer_mv
		}
	}

	if enableOrderHint == 1 {
		br.readBits(3) // order_hint_bits_minus_1
	}

	br.readBits(1) // enable_superres
	br.readBits(1) // enable_cdef
	br.readBits(1) // enable_restoration

	// color_config
	info.highBitdepth = byte(br.readBits(1))
	if info.Profile == 2 && info.highBitdepth == 1 {
		info.twelveBit = byte(br.readBits(1))
	}

	info.BitDepth = 8
	if info.highBitdepth == 1 {
		if info.twelveBit == 1 {
			info.BitDepth = 12
		} else {
			info.BitDepth = 10
		}
	}

	if info.Profile != 1 {
		if br.readBits(1) == 1 {
			info.Monochrome = true
		}
	}

	// color_description_present_flag
	var colorPrimaries, transferChars, matrixCoeffs uint32 = 2, 2, 2 // unspecified
	if br.readBits(1) == 1 {
		colorPrimaries = br.readBits(8)
		transferChars = br.readBits(8)
		matrixCoeffs = br.readBits(8)
	}

	// AV1 spec 5.5.2: the subsampling is implied by seq_profile and bit depth,
	// and only coded for 12-bit profile 2
	switch {
	case info.Monochrome:
		br.readBits(1) // color_range
		info.ChromaSubsamplingX = 1
		info.ChromaSubsamplingY = 1
	case colorPrimaries == 1 && transferChars == 13 && matrixCoeffs == 0:
		// sRGB with the identity matrix is 4:4:4 full range, no color_range bit
		info.ChromaSubsamplingX = 0
		info.ChromaSubsamplingY = 0
	default:
		br.readBits(1) // color_range
		switch {
		case info.Profile == 0:
			info.ChromaSubsamplingX = 1
			info.ChromaSubsamplingY = 1
		case info.Profile == 1:
			info.ChromaSubsamplingX = 0
			info.ChromaSubsamplingY = 0
		case info.BitDepth == 12:
			info.ChromaSubsamplingX = byte(br.readBits(1))
			if info.ChromaSubsamplingX == 1 {
				info.ChromaSubsamplingY = byte(br.readBits(1))
			} else {
				info.ChromaSubsamplingY = 0
			}
		default:
			info.ChromaSubsamplingX = 1
			info.ChromaSubsamplingY = 0
		}
		if info.ChromaSubsamplingX == 1 && info.ChromaSubsamplingY == 1 {
			info.ChromaSamplePos = byte(br.readBits(2))
		}
	}

	// a header that ran off the end parsed into garbage, and callers rely on
	// nil to fall back instead of writing a 1x1 track
	if br.eof {
		return nil
	}

	return info
}

// minLevelForResolution returns the minimum AV1 seq_level_idx required for
// the given resolution, assuming up to 30fps. Camera firmware may declare a
// level that is too low for the actual content (e.g., Level 5.0 for 4K@30fps)
// which causes browsers to limit the decoder.
//
// AV1 level constraints (Section A.3 of the AV1 spec):
//
//	Level 4.1 (idx  9): MaxPicSize  2,228,224 (1080p@30fps)
//	Level 5.1 (idx 13): MaxPicSize  8,912,896 (4K@30fps)
//	Level 6.1 (idx 17): MaxPicSize 35,651,584 (8K@30fps)
func minLevelForResolution(width, height uint16) byte {
	pixels := uint32(width) * uint32(height)
	switch {
	case pixels > 8_912_896:
		return 17 // Level 6.1
	case pixels > 2_228_224:
		return 13 // Level 5.1
	case pixels > 1_065_024:
		return 9 // Level 4.1
	default:
		return 0
	}
}

// MimeCodecString generates the av01 MIME codec string from a raw Sequence Header OBU.
// Format: av01.<profile>.<levelIdx><tier>.<bitDepth>
// See: https://aomediacodec.github.io/av1-isobmff/#codecsparam
//
// The level is validated against the actual resolution to ensure the browser
// allocates the correct decoder resources. Some cameras (e.g., UniFi) declare
// Level 5.0 for 4K@30fps content which actually needs 5.1.
func MimeCodecString(seqHdr []byte) string {
	info := ParseSequenceHeaderInfo(seqHdr)
	if info == nil {
		return ""
	}

	level := info.Level
	if minLevel := minLevelForResolution(info.Width, info.Height); level < minLevel {
		level = minLevel
	}

	tierChar := 'M'
	if info.Tier == 1 {
		tierChar = 'H'
	}

	return fmt.Sprintf("av01.%d.%02d%c.%02d", info.Profile, level, tierChar, info.BitDepth)
}

// EncodeConfig creates an AV1CodecConfigurationRecord for the av1C box in MP4.
// See: https://aomediacodec.github.io/av1-isobmff/#av1codecconfigurationrecord
//
// The seqHdr should be a raw Sequence Header OBU (with OBU header and size).
// If seqHdr is nil, a default config (Main profile, level 4.0, 8-bit) is used.
func EncodeConfig(seqHdr []byte) []byte {
	var profile, level, tier byte
	var highBitdepth, twelveBit, monoFlag byte
	var chromaX, chromaY, chromaPos byte

	if info := ParseSequenceHeaderInfo(seqHdr); info != nil {
		profile = info.Profile
		level = info.Level
		tier = info.Tier
		highBitdepth = info.highBitdepth
		twelveBit = info.twelveBit
		if info.Monochrome {
			monoFlag = 1
		}
		chromaX = info.ChromaSubsamplingX
		chromaY = info.ChromaSubsamplingY
		chromaPos = info.ChromaSamplePos
	} else {
		// defaults: Main profile, level 4.0 (idx=8), Main tier, 8-bit, 4:2:0
		level = 8
		chromaX = 1
		chromaY = 1
	}

	conf := []byte{
		0x81, // marker=1, version=1
		(profile << 5) | (level & 0x1F),
		(tier << 7) | (highBitdepth << 6) | (twelveBit << 5) | (monoFlag << 4) | (chromaX << 3) | (chromaY << 2) | (chromaPos & 0x03),
		0x00, // reserved(3)=0, initial_presentation_delay_present=0, reserved(4)=0
	}

	if seqHdr != nil {
		conf = append(conf, seqHdr...)
	}

	return conf
}

// ConfigToCodec parses an AV1CodecConfigurationRecord (av1C) into a core.Codec.
// The record is carried in the Enhanced-RTMP/FLV PacketTypeSequenceStart body
// and in the ISOBMFF av1C box. The sequence header OBU from configOBUs is kept
// raw in FmtpLine, the same convention the MP4 consumer uses, so EncodeConfig
// can write it back out unchanged.
func ConfigToCodec(conf []byte) *core.Codec {
	codec := &core.Codec{
		Name:        core.CodecAV1,
		ClockRate:   90000,
		PayloadType: core.PayloadTypeRAW,
	}
	if len(conf) > 4 {
		if seqHdr := SequenceHeader(conf[4:]); seqHdr != nil {
			codec.FmtpLine = string(seqHdr)
		}
	}
	return codec
}

// DecodeSequenceHeader parses a Sequence Header OBU and returns width and height.
// Convenience wrapper around ParseSequenceHeaderInfo.
func DecodeSequenceHeader(obu []byte) (width, height uint16) {
	if info := ParseSequenceHeaderInfo(obu); info != nil {
		return info.Width, info.Height
	}
	return 0, 0
}

// bitReader is a simple bit-level reader.
type bitReader struct {
	data   []byte
	offset int  // bit offset
	eof    bool // set when a read ran past the end of data
}

func (r *bitReader) readBits(n uint32) uint32 {
	var val uint32
	for i := uint32(0); i < n; i++ {
		byteIdx := r.offset / 8
		bitIdx := 7 - (r.offset % 8)
		if byteIdx >= len(r.data) {
			r.eof = true
			return val
		}
		val = (val << 1) | uint32((r.data[byteIdx]>>uint(bitIdx))&1)
		r.offset++
	}
	return val
}

func (r *bitReader) readUVLC() uint32 {
	leadingZeros := uint32(0)
	for {
		if r.readBits(1) == 1 {
			break
		}
		leadingZeros++
		if leadingZeros > 32 {
			return 0
		}
	}
	if leadingZeros == 0 {
		return 0
	}
	return (1 << leadingZeros) - 1 + r.readBits(leadingZeros)
}
