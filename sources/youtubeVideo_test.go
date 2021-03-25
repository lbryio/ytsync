package sources

import (
	"testing"

	"github.com/abadojack/whatlanggo"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestLanguageDetection(t *testing.T) {
	description := `Om lättkränkta muslimer, och den bristande logiken i vad som anses vara att vanära profeten. Från Moderata riksdagspolitikern Hanif Balis podcast "God Ton", avsnitt 108, från oktober 2020, efter terrordådet där en fransk lärare fick huvudet avskuret efter att undervisat sin mångkulturella klass om frihet.`
	info := whatlanggo.Detect(description)
	logrus.Infof("confidence: %.2f", info.Confidence)
	assert.True(t, info.IsReliable())
	assert.True(t, info.Lang.Iso6391() != "")
	assert.Equal(t, "sv", info.Lang.Iso6391())

	description = `🥳週四直播 | 晚上來開個賽車🔰歡迎各位一起來玩! - PonPonLin蹦蹦林`
	info = whatlanggo.Detect(description)
	logrus.Infof("confidence: %.2f", info.Confidence)
	assert.True(t, info.IsReliable())
	assert.True(t, info.Lang.Iso6391() != "")
	assert.Equal(t, "zh", info.Lang.Iso6391())
}
