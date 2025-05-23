package unix

import (
	"bufio"
	"net"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/pkg/formats/mpegts"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/defs"
	"github.com/bluenviron/mediamtx/internal/test"
)

func TestSource(t *testing.T) {
	te := test.NewSourceTester(
		func(p defs.StaticSourceParent) defs.StaticSource {
			return &Source{
				ReadTimeout: conf.Duration(10 * time.Second),
				Parent:      p,
			}
		},
		"unix:/tmp/mediamtx-test.sock",
		&conf.Path{},
	)
	defer te.Close()

	time.Sleep(50 * time.Millisecond)

	conn, err := net.Dial("unix", "/tmp/mediamtx-test.sock")
	require.NoError(t, err)
	defer conn.Close()

	track := &mpegts.Track{
		Codec: &mpegts.CodecH264{},
	}

	bw := bufio.NewWriter(conn)
	w := mpegts.NewWriter(bw, []*mpegts.Track{track})
	require.NoError(t, err)

	err = w.WriteH2642(track, 0, 0, [][]byte{{ // IDR
		5, 1,
	}})
	require.NoError(t, err)

	err = w.WriteH2642(track, 0, 0, [][]byte{{ // non-IDR
		5, 2,
	}})
	require.NoError(t, err)

	err = bw.Flush()
	require.NoError(t, err)

	<-te.Unit
}
