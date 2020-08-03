package ytapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChannelInfo(t *testing.T) {
	_, _, err := ChannelInfo("", "UCNQfQvFMPnInwsU_iGYArJQ")
	assert.NoError(t, err)
}
