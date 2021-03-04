package namer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_getClaimNameFromTitle(t *testing.T) {
	name := getClaimNameFromTitle("СтопХам - \"В ожидании ответа\"", 0)
	assert.Equal(t, name, "стопхам-в-ожидании")
	name = getClaimNameFromTitle("SADB - \"A Weak Woman With a Strong Hood\"", 0)
	assert.Equal(t, name, "sadb-a-weak-woman-with-a-strong-hood")
	name = getClaimNameFromTitle("錢包整理術 5 Tips、哪種錢包最NG？｜有錢人默默在做的「錢包整理術」 ft.@SHIN LI", 0)
	assert.Equal(t, name, "錢包整理術-5-tips、哪種錢包最")
}
