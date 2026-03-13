package stream

import (
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/unit"
)

func makeUnit(pts int64, payload unit.Payload) *unit.Unit {
	return &unit.Unit{
		PTS: pts,
		NTP: time.Now(),
		RTPPackets: []*rtp.Packet{{
			Header:  rtp.Header{Timestamp: uint32(pts)},
			Payload: []byte{1, 2, 3},
		}},
		Payload: payload,
	}
}

func TestGopCacheAdd(t *testing.T) {
	c := &gopCache{parent: nilLogger{}}

	// Non-keyframe before any keyframe → not cached.
	c.add(makeUnit(0, unit.PayloadH264{{0x41, 1}}), false)
	require.Nil(t, c.snapshot())

	// Keyframe → cached.
	c.add(makeUnit(1000, unit.PayloadH264{{5, 1}}), true)
	snap := c.snapshot()
	require.Len(t, snap, 1)
	require.Equal(t, int64(1000), snap[0].PTS)

	// Subsequent non-keyframes → appended.
	c.add(makeUnit(2000, unit.PayloadH264{{0x41, 1}}), false)
	c.add(makeUnit(3000, unit.PayloadH264{{0x41, 2}}), false)
	c.add(makeUnit(4000, unit.PayloadH264{{0x41, 3}}), false)
	snap = c.snapshot()
	require.Len(t, snap, 4)

	// Verify RTP packets are cloned (different pointer).
	origPkt := &rtp.Packet{Header: rtp.Header{Timestamp: 99}, Payload: []byte{9}}
	u := &unit.Unit{
		PTS:        5000,
		RTPPackets: []*rtp.Packet{origPkt},
		Payload:    unit.PayloadH264{{0x41, 4}},
	}
	c.add(u, false)
	snap = c.snapshot()
	cachedPkt := snap[len(snap)-1].RTPPackets[0]
	require.NotSame(t, origPkt, cachedPkt)
	require.Equal(t, origPkt.Payload, cachedPkt.Payload)
}

func TestGopCacheKeyFrameReset(t *testing.T) {
	c := &gopCache{parent: nilLogger{}}

	// Build a GOP: keyframe + 5 non-keyframes.
	c.add(makeUnit(0, unit.PayloadH264{{5, 1}}), true)
	for i := 1; i <= 5; i++ {
		c.add(makeUnit(int64(i*1000), unit.PayloadH264{{0x41, byte(i)}}), false)
	}
	require.Len(t, c.snapshot(), 6)

	// New keyframe → resets cache.
	c.add(makeUnit(10000, unit.PayloadH264{{5, 2}}), true)
	require.Len(t, c.snapshot(), 1)
	require.Equal(t, int64(10000), c.snapshot()[0].PTS)

	// Continue appending.
	c.add(makeUnit(11000, unit.PayloadH264{{0x41, 1}}), false)
	c.add(makeUnit(12000, unit.PayloadH264{{0x41, 2}}), false)
	require.Len(t, c.snapshot(), 3)
}

func TestGopCacheOverflow(t *testing.T) {
	c := &gopCache{parent: nilLogger{}}

	c.add(makeUnit(0, unit.PayloadH264{{5, 1}}), true)
	for i := 1; i <= 600; i++ {
		c.add(makeUnit(int64(i), unit.PayloadH264{{0x41, 1}}), false)
	}

	snap := c.snapshot()
	require.Len(t, snap, gopCacheMaxSize)
	// First entry should be unit #89 (601 total - 512 max = discard first 89).
	require.Equal(t, int64(89), snap[0].PTS)
}

func TestGopCacheSnapshotIndependent(t *testing.T) {
	c := &gopCache{parent: nilLogger{}}

	c.add(makeUnit(0, unit.PayloadH264{{5, 1}}), true)
	c.add(makeUnit(1000, unit.PayloadH264{{0x41, 1}}), false)

	snap1 := c.snapshot()
	require.Len(t, snap1, 2)

	// Add more units after taking snapshot.
	c.add(makeUnit(2000, unit.PayloadH264{{0x41, 2}}), false)
	c.add(makeUnit(3000, unit.PayloadH264{{0x41, 3}}), false)

	// First snapshot should be unchanged.
	require.Len(t, snap1, 2)

	snap2 := c.snapshot()
	require.Len(t, snap2, 4)
}

func TestIsVideoKeyFrame(t *testing.T) {
	h264Format := &format.H264{}
	h265Format := &format.H265{}
	av1Format := &format.AV1{}

	for _, ca := range []struct {
		name   string
		forma  format.Format
		payload unit.Payload
		expect bool
	}{
		{"h264 IDR", h264Format, unit.PayloadH264{{5, 1}}, true},
		{"h264 non-IDR", h264Format, unit.PayloadH264{{0x41, 1}}, false},
		{"h265 IDR_W_RADL", h265Format, unit.PayloadH265{{0x26, 1}}, true},
		{"h265 non-IDR", h265Format, unit.PayloadH265{{0x02, 1}}, false},
		{"av1 sequence header", av1Format, unit.PayloadAV1{{0x0a, 1}}, true},
		{"av1 frame", av1Format, unit.PayloadAV1{{0x32, 1}}, false},
	} {
		t.Run(ca.name, func(t *testing.T) {
			require.Equal(t, ca.expect, isVideoKeyFrame(ca.forma, ca.payload))
		})
	}
}

func TestIsVideoFormat(t *testing.T) {
	require.True(t, isVideoFormat(&format.H264{}))
	require.True(t, isVideoFormat(&format.H265{}))
	require.True(t, isVideoFormat(&format.AV1{}))
	require.False(t, isVideoFormat(&format.Opus{}))
	require.False(t, isVideoFormat(&format.VP9{}))
}
