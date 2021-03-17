package namer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_getClaimNameFromTitle(t *testing.T) {
	name := getClaimNameFromTitle("СтопХам - \"В ожидании ответа\"", 0)
	assert.Equal(t, "стопхам-в-ожидании", name)
	name = getClaimNameFromTitle("SADB - \"A Weak Woman With a Strong Hood\"", 0)
	assert.Equal(t, "sadb-a-weak-woman-with-a-strong-hood", name)
	name = getClaimNameFromTitle("錢包整理術 5 Tips、哪種錢包最NG？｜有錢人默默在做的「錢包整理術」 ft.@SHIN LI", 0)
	assert.Equal(t, "錢包整理術-5-tips、哪種錢包最", name)
	name = getClaimNameFromTitle("اسرع-طريقة-لتختيم", 0)
	assert.Equal(t, "اسرع-طريقة-لتختيم", name)
	name = getClaimNameFromTitle("شكرا على 380 مشترك😍😍😍😍 لي يريد دعم ادا وصلنا المقطع 40 لايك وراح ادعم قناتين", 0)
	assert.Equal(t, "شكرا-على-380-مشترك😍😍\xf0\x9f", name)
	name = getClaimNameFromTitle("test-@", 0)
	assert.Equal(t, "test", name)
	name = getClaimNameFromTitle("『あなたはただの空の殻でした』", 0)
	assert.Equal(t, "『あなたはただの空の殻でした』", name)
	name = getClaimNameFromTitle("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 50)
	assert.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-50", name)
}
