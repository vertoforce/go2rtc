package av1

import (
	"encoding/hex"
	"testing"
)

// seqHdrWriter builds a sequence header OBU bit by bit.
type seqHdrWriter struct {
	buf  []byte
	bits int
}

func (w *seqHdrWriter) write(value uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		if w.bits%8 == 0 {
			w.buf = append(w.buf, 0)
		}
		if (value>>uint(i))&1 == 1 {
			w.buf[len(w.buf)-1] |= 1 << uint(7-w.bits%8)
		}
		w.bits++
	}
}

func (w *seqHdrWriter) obu() []byte {
	obu := []byte{0x0A} // type=1 (sequence header), has_size=1
	obu = append(obu, WriteLEB128(uint32(len(w.buf)))...)
	return append(obu, w.buf...)
}

// buildSeqHdr writes a valid 1920x1080 8-bit 4:2:0 sequence header OBU.
// timing adds timing_info and decoder_model_info, delay adds
// initial_display_delay_present_flag, both of which carry extra per
// operating point fields ahead of the frame dimensions.
func buildSeqHdr(timing, delay bool) []byte {
	w := &seqHdrWriter{}
	w.write(0, 3) // seq_profile
	w.write(0, 1) // still_picture
	w.write(0, 1) // reduced_still_picture_header

	if timing {
		w.write(1, 1)  // timing_info_present_flag
		w.write(1, 32) // num_units_in_display_tick
		w.write(30, 32)
		w.write(0, 1) // equal_picture_interval
		w.write(1, 1) // decoder_model_info_present_flag
		w.write(9, 5) // buffer_delay_length_minus_1
		w.write(1, 32)
		w.write(0, 5)
		w.write(0, 5)
	} else {
		w.write(0, 1) // timing_info_present_flag
	}

	if delay {
		w.write(1, 1) // initial_display_delay_present_flag
	} else {
		w.write(0, 1)
	}

	w.write(0, 5)  // operating_points_cnt_minus_1
	w.write(0, 12) // operating_point_idc[0]
	w.write(8, 5)  // seq_level_idx[0], above 7 so a tier bit follows
	w.write(0, 1)  // seq_tier[0]

	if timing {
		w.write(1, 1)  // decoder_model_present_for_this_op[0]
		w.write(0, 10) // decoder_buffer_delay
		w.write(0, 10) // encoder_buffer_delay
		w.write(0, 1)  // low_delay_mode_flag
	}
	if delay {
		w.write(1, 1) // initial_display_delay_present_for_this_op[0]
		w.write(0, 4) // initial_display_delay_minus_1[0]
	}

	w.write(10, 4)    // frame_width_bits_minus_1
	w.write(10, 4)    // frame_height_bits_minus_1
	w.write(1919, 11) // max_frame_width_minus_1
	w.write(1079, 11) // max_frame_height_minus_1

	w.write(0, 1) // frame_id_numbers_present_flag
	w.write(1, 1) // use_128x128_superblock
	w.write(1, 1) // enable_filter_intra
	w.write(1, 1) // enable_intra_edge_filter
	w.write(0, 4) // interintra, masked compound, warped motion, dual filter
	w.write(1, 1) // enable_order_hint
	w.write(0, 1) // enable_jnt_comp
	w.write(0, 1) // enable_ref_frame_mvs
	w.write(1, 1) // seq_choose_screen_content_tools
	w.write(1, 1) // seq_choose_integer_mv
	w.write(6, 3) // order_hint_bits_minus_1
	w.write(0, 1) // enable_superres
	w.write(1, 1) // enable_cdef
	w.write(1, 1) // enable_restoration

	// color_config
	w.write(0, 1) // high_bitdepth
	w.write(0, 1) // mono_chrome
	w.write(0, 1) // color_description_present_flag
	w.write(0, 1) // color_range
	w.write(0, 2) // chroma_sample_position
	w.write(0, 1) // separate_uv_delta_q
	w.write(1, 1) // trailing one bit

	return w.obu()
}

// TestSequenceHeaderOperatingPoints covers the fields the operating point loop
// has to skip. Missing them desyncs the bit reader and the frame dimensions
// read as garbage.
func TestSequenceHeaderOperatingPoints(t *testing.T) {
	for _, tt := range []struct {
		name          string
		timing, delay bool
	}{
		{"plain", false, false},
		{"initial_display_delay", false, true},
		{"decoder_model", true, false},
		{"both", true, true},
	} {
		w, h := DecodeSequenceHeader(buildSeqHdr(tt.timing, tt.delay))
		if w != 1920 || h != 1080 {
			t.Errorf("%s: got %dx%d, want 1920x1080", tt.name, w, h)
		}
	}
}

// TestSequenceHeaderTruncated checks that a short header parses to nothing.
// The muxer only falls back to a default size when width is zero, so a
// partial parse would write a 1x1 video track.
func TestSequenceHeaderTruncated(t *testing.T) {
	obu := buildSeqHdr(false, false)

	for i := 2; i < len(obu)-1; i++ {
		trunc := obu[:i]
		if w, h := DecodeSequenceHeader(trunc); w != 0 || h != 0 {
			t.Errorf("%d bytes: got %dx%d, want 0x0", i, w, h)
		}
		if ParseSequenceHeaderInfo(trunc) != nil {
			t.Errorf("%d bytes: got info, want nil", i)
		}
	}
}

// TestSequenceHeaderRealStreams parses av1C records taken from the
// moov>trak>stsd>av01>av1C box of go2rtc stream.mp4 output, produced by
// ffmpeg av1_nvenc from six cameras.
func TestSequenceHeaderRealStreams(t *testing.T) {
	for _, tt := range []struct {
		name          string
		av1c          string
		width, height uint16
	}{
		{"portrait_1080x1920", "81080c000a0e00000042aa1bf7f0086640404041", 1080, 1920},
		{"wide_4096x1552", "810c0c000a0c0000006329fff83c02198040", 4096, 1552},
		{"2560x1920", "810c0c000a0c00000062ea7ffbf804330080", 2560, 1920},
		{"4k_3840x2160", "810c0c000a0c00000062efbfe1bc02198040", 3840, 2160},
		{"1080p", "81080c000a0e00000042abbfc370086640404041", 1920, 1080},
	} {
		conf, err := hex.DecodeString(tt.av1c)
		if err != nil {
			t.Fatal(err)
		}

		seqHdr := conf[4:] // skip the av1C fixed header

		if w, h := DecodeSequenceHeader(seqHdr); w != tt.width || h != tt.height {
			t.Errorf("%s: got %dx%d, want %dx%d", tt.name, w, h, tt.width, tt.height)
		}

		// re-encoding has to reproduce the record ffmpeg wrote
		if re := EncodeConfig(seqHdr); string(re) != string(conf) {
			t.Errorf("%s: re-encoded %x, want %x", tt.name, re, conf)
		}

		codec := ConfigToCodec(conf)
		if codec.FmtpLine != string(seqHdr) {
			t.Errorf("%s: ConfigToCodec kept %x, want %x", tt.name, codec.FmtpLine, seqHdr)
		}
	}
}
