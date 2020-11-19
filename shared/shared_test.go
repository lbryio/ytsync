package shared

import (
	"testing"

	"gotest.tools/assert"
)

func TestSyncFlags_VideosToSync(t *testing.T) {
	f := SyncFlags{}
	assert.Equal(t, f.VideosToSync(0), 0)
	assert.Equal(t, f.VideosToSync(1), 10)
	assert.Equal(t, f.VideosToSync(5), 10)
	assert.Equal(t, f.VideosToSync(10), 10)
	assert.Equal(t, f.VideosToSync(101), 50)
	assert.Equal(t, f.VideosToSync(500), 80)
	assert.Equal(t, f.VideosToSync(21000), 1000)
	f.VideosLimit = 1337
	assert.Equal(t, f.VideosToSync(21), 1337)
}
