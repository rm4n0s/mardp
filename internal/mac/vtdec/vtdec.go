// Package vtdec decodes H.264 with VideoToolbox, into pictures: what a
// client shows of the stream the host sends. The host itself never
// decodes; the proofs that the picture is right do (SPEC §15.5), since a
// frame count does not show whether letters can be read.
package vtdec

// The failures of the package.
const (
	// TagDecoderUnsupported: the decoder runs only on macOS.
	TagDecoderUnsupported = "DecoderUnsupported"
	// TagDecoderNoParameters: a frame came before any key frame with the
	// stream's parameter sets.
	TagDecoderNoParameters = "DecoderNoParameters"
	// TagDecoderCreateFailed: VideoToolbox refused the decoder.
	TagDecoderCreateFailed = "DecoderCreateFailed"
	// TagDecoderFrameInvalid: an access unit has no picture in it.
	TagDecoderFrameInvalid = "DecoderFrameInvalid"
	// TagDecoderFailed: VideoToolbox failed to decode a frame.
	TagDecoderFailed = "DecoderFailed"
)

// NAL unit types of H.264.
const (
	nalSlice = 1
	nalIDR   = 5
	nalSPS   = 7
	nalPPS   = 8
)

// unit is what one access unit holds.
type unit struct {
	sps, pps []byte
	// avcc is its slices, each after its length in 4 bytes.
	avcc []byte
}

// parse splits an access unit in Annex B, with start codes of 3 or 4
// bytes, into its parameter sets and its slices.
func parse(annexB []byte) unit {
	var u unit
	for _, nal := range split(annexB) {
		switch nal[0] & 0x1f {
		case nalSPS:
			u.sps = nal
		case nalPPS:
			u.pps = nal
		case nalSlice, nalIDR:
			n := len(nal)
			u.avcc = append(u.avcc, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
			u.avcc = append(u.avcc, nal...)
		}
	}
	return u
}

// split returns the NAL units of b, without their start codes.
func split(b []byte) [][]byte {
	var nals [][]byte
	start := -1
	for i := 0; i+2 < len(b); i++ {
		if b[i] != 0 || b[i+1] != 0 || b[i+2] != 1 {
			continue
		}
		if start >= 0 {
			end := i
			if end > start && b[end-1] == 0 {
				end-- // the first byte of a 4-byte start code
			}
			if end > start {
				nals = append(nals, b[start:end])
			}
		}
		start = i + 3
		i += 2
	}
	if start >= 0 && start < len(b) {
		nals = append(nals, b[start:])
	}
	return nals
}
