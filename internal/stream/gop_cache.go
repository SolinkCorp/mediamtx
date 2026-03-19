package stream

import (
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format"
	mcav1 "github.com/bluenviron/mediacommon/v2/pkg/codecs/av1"
	mch264 "github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	mch265 "github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/pion/rtp"

	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/bluenviron/mediamtx/internal/unit"
)

const gopCacheMaxSize = 512

// GopCachedUnit is a single cached video unit in the GOP cache.
type GopCachedUnit struct {
	PTS        int64
	NTP        time.Time
	RTPPackets []*rtp.Packet
	Payload    unit.Payload
}

type gopCache struct {
	mu           sync.Mutex
	units        []*GopCachedUnit
	overflowOnce bool
	parent       logger.Writer
}

func (c *gopCache) add(u *unit.Unit, isKeyFrame bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if isKeyFrame {
		// Clear cached units on keyframe. The keyframe AU itself contains SPS/PPS/VPS
		// because this is called after the unitRemuxer, which prepends parameter sets
		// to keyframe access units (see unitRemuxerH264 and unitRemuxerH265).
		c.units = c.units[:0]
		c.overflowOnce = false
	}

	// Only cache if we've seen at least one keyframe.
	if len(c.units) == 0 && !isKeyFrame {
		return
	}

	// Clone RTP packets since gortsplib may mutate headers in-place.
	clonedPkts := make([]*rtp.Packet, len(u.RTPPackets))
	for i, pkt := range u.RTPPackets {
		clone := *pkt
		clone.Payload = append([]byte(nil), pkt.Payload...)
		clonedPkts[i] = &clone
	}

	c.units = append(c.units, &GopCachedUnit{
		PTS:        u.PTS,
		NTP:        u.NTP,
		RTPPackets: clonedPkts,
		Payload:    u.Payload,
	})

	if len(c.units) > gopCacheMaxSize {
		if !c.overflowOnce {
			c.overflowOnce = true
			c.parent.Log(logger.Warn,
				"GOP cache overflow: more than %d units cached, oldest units discarded", gopCacheMaxSize)
		}
		copy(c.units, c.units[len(c.units)-gopCacheMaxSize:])
		c.units = c.units[:gopCacheMaxSize]
	}
}

// snapshot returns a copy of the cached units slice.
func (c *gopCache) snapshot() []*GopCachedUnit {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.units) == 0 {
		return nil
	}

	out := make([]*GopCachedUnit, len(c.units))
	copy(out, c.units)
	return out
}

func isVideoKeyFrame(payload unit.Payload) bool {
	switch payload := payload.(type) {
	case unit.PayloadH264:
		return mch264.IsRandomAccess(payload)
	case unit.PayloadH265:
		return mch265.IsRandomAccess(payload)
	case unit.PayloadAV1:
		return mcav1.IsRandomAccess2(payload)
	default:
		return false
	}
}

func isVideoFormat(forma format.Format) bool {
	switch forma.(type) {
	case *format.H264, *format.H265, *format.AV1:
		return true
	default:
		return false
	}
}
