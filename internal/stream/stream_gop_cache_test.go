package stream

import (
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediamtx/internal/unit"
	"github.com/stretchr/testify/require"
)

func testMediaH264() *description.Media {
	return &description.Media{
		Type: description.MediaTypeVideo,
		Formats: []format.Format{&format.H264{
			PayloadTyp:        96,
			PacketizationMode: 1,
		}},
	}
}

func testMediaH265() *description.Media {
	return &description.Media{
		Type: description.MediaTypeVideo,
		Formats: []format.Format{&format.H265{
			PayloadTyp: 96,
		}},
	}
}

func testMediaMPEG4Audio() *description.Media {
	return &description.Media{
		Type: description.MediaTypeAudio,
		Formats: []format.Format{&format.MPEG4Audio{
			PayloadTyp: 96,
			Config: &mpeg4audio.AudioSpecificConfig{
				Type:          2,
				SampleRate:    44100,
				ChannelCount:  2,
				ChannelConfig: 2,
			},
			SizeLength:       13,
			IndexLength:      3,
			IndexDeltaLength: 3,
		}},
	}
}

func TestStreamGopCacheReplay(t *testing.T) {
	for _, ca := range []struct {
		name            string
		media           *description.Media
		keyFramePayload unit.Payload
		nonKeyPayload   unit.Payload
	}{
		{
			"h264",
			testMediaH264(),
			unit.PayloadH264{{5, 1}},
			unit.PayloadH264{{0x41, 1}},
		},
		{
			"h265",
			testMediaH265(),
			unit.PayloadH265{{0x26, 1}},
			unit.PayloadH265{{0x02, 1}},
		},
	} {
		t.Run(ca.name, func(t *testing.T) {
			desc := &description.Session{Medias: []*description.Media{ca.media}}

			strm := &Stream{
				Desc:              desc,
				WriteQueueSize:    512,
				RTPMaxPayloadSize: 1450,
				GopCache:          true,
				Parent:            nilLogger{},
			}
			err := strm.Initialize()
			require.NoError(t, err)
			defer strm.Close()

			subStream := &SubStream{
				Stream: strm,
			}
			err = subStream.Initialize()
			require.NoError(t, err)

			medi := desc.Medias[0]
			forma := medi.Formats[0]

			// Write keyframe + 4 non-keyframes before any reader.
			subStream.WriteUnit(medi, forma, &unit.Unit{
				PTS:     1000,
				Payload: ca.keyFramePayload,
			})
			for i := 1; i <= 4; i++ {
				subStream.WriteUnit(medi, forma, &unit.Unit{
					PTS:     int64(1000 + i*1000),
					Payload: ca.nonKeyPayload,
				})
			}

			// Now add a reader — it should receive the 5 cached units via replay,
			// plus one live unit we write after.
			var mu sync.Mutex
			var received []*unit.Unit

			allDone := make(chan struct{})

			r := &Reader{Parent: nilLogger{}}
			r.OnData(medi, forma, func(u *unit.Unit) error {
				mu.Lock()
				received = append(received, u)
				count := len(received)
				mu.Unlock()

				// 5 cached + 1 live = 6 total.
				if count == 6 {
					close(allDone)
				}
				return nil
			})

			strm.AddReader(r)
			defer strm.RemoveReader(r)

			// Write one live unit after reader is added.
			subStream.WriteUnit(medi, forma, &unit.Unit{
				PTS:     10000,
				Payload: ca.nonKeyPayload,
			})

			select {
			case <-allDone:
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for units")
			}

			mu.Lock()
			defer mu.Unlock()
			require.Len(t, received, 6)

			// First 5 should be replayed with compressed PTS (900-tick spacing).
			for i := 1; i < 5; i++ {
				delta := received[i].PTS - received[i-1].PTS
				require.Equal(t, int64(900), delta, "replay frame %d PTS delta", i)
			}
		})
	}
}

func TestStreamGopCacheDisabled(t *testing.T) {
	desc := &description.Session{Medias: []*description.Media{
		{
			Type:    description.MediaTypeVideo,
			Formats: []format.Format{&format.H264{}},
		},
	}}

	strm := &Stream{
		Desc:              desc,
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		GopCache:          false,
		Parent:            nilLogger{},
	}
	err := strm.Initialize()
	require.NoError(t, err)
	defer strm.Close()

	subStream := &SubStream{
		Stream: strm,
	}
	err = subStream.Initialize()
	require.NoError(t, err)

	medi := desc.Medias[0]
	forma := medi.Formats[0]

	// Write keyframe + frame before reader.
	subStream.WriteUnit(medi, forma, &unit.Unit{
		PTS:     1000,
		Payload: unit.PayloadH264{{5, 1}},
	})
	subStream.WriteUnit(medi, forma, &unit.Unit{
		PTS:     2000,
		Payload: unit.PayloadH264{{0x41, 1}},
	})

	// Add reader — should NOT receive cached units.
	recv := make(chan *unit.Unit, 10)

	r := &Reader{Parent: nilLogger{}}
	r.OnData(medi, forma, func(u *unit.Unit) error {
		recv <- u
		return nil
	})

	strm.AddReader(r)
	defer strm.RemoveReader(r)

	// Write one live unit.
	subStream.WriteUnit(medi, forma, &unit.Unit{
		PTS:     5000,
		Payload: unit.PayloadH264{{5, 2}},
	})

	select {
	case u := <-recv:
		require.NotNil(t, u.Payload)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for live unit")
	}

	// Drain briefly to check no extra units arrive.
	time.Sleep(100 * time.Millisecond)
	require.Len(t, recv, 0, "should not have received cached units")
}

func TestStreamGopCacheVideoOnly(t *testing.T) {
	videoMedia := testMediaH264()
	audioMedia := testMediaMPEG4Audio()

	desc := &description.Session{Medias: []*description.Media{videoMedia, audioMedia}}

	strm := &Stream{
		Desc:              desc,
		WriteQueueSize:    512,
		RTPMaxPayloadSize: 1450,
		GopCache:          true,
		Parent:            nilLogger{},
	}
	err := strm.Initialize()
	require.NoError(t, err)
	defer strm.Close()

	subStream := &SubStream{
		Stream: strm,
	}
	err = subStream.Initialize()
	require.NoError(t, err)

	videoFormat := videoMedia.Formats[0]
	audioFormat := audioMedia.Formats[0]

	// Write video keyframe + audio frame before reader.
	subStream.WriteUnit(videoMedia, videoFormat, &unit.Unit{
		PTS:     1000,
		Payload: unit.PayloadH264{{5, 1}},
	})
	subStream.WriteUnit(audioMedia, audioFormat, &unit.Unit{
		PTS:     1000,
		Payload: unit.PayloadMPEG4Audio{{0x01, 0x02}},
	})
	subStream.WriteUnit(videoMedia, videoFormat, &unit.Unit{
		PTS:     2000,
		Payload: unit.PayloadH264{{0x41, 1}},
	})

	// Add reader for both video and audio.
	videoRecv := make(chan *unit.Unit, 10)
	audioRecv := make(chan *unit.Unit, 10)

	r := &Reader{Parent: nilLogger{}}
	r.OnData(videoMedia, videoFormat, func(u *unit.Unit) error {
		videoRecv <- u
		return nil
	})
	r.OnData(audioMedia, audioFormat, func(u *unit.Unit) error {
		audioRecv <- u
		return nil
	})

	strm.AddReader(r)
	defer strm.RemoveReader(r)

	// Write live units.
	subStream.WriteUnit(videoMedia, videoFormat, &unit.Unit{
		PTS:     5000,
		Payload: unit.PayloadH264{{5, 2}},
	})
	subStream.WriteUnit(audioMedia, audioFormat, &unit.Unit{
		PTS:     5000,
		Payload: unit.PayloadMPEG4Audio{{0x03, 0x04}},
	})

	// Wait for live video.
	timeout := time.After(5 * time.Second)
	var videoUnits []*unit.Unit
	for {
		select {
		case u := <-videoRecv:
			videoUnits = append(videoUnits, u)
			// 2 cached video + 1 live video = 3.
			if len(videoUnits) == 3 {
				goto done
			}
		case <-timeout:
			t.Fatalf("timed out, got %d video units", len(videoUnits))
		}
	}
done:

	require.Len(t, videoUnits, 3)

	// Audio should NOT have any cached replay — only the live one.
	time.Sleep(100 * time.Millisecond)
	require.Len(t, audioRecv, 1, "audio should not be cached/replayed")
}
