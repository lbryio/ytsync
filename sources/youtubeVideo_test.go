package sources

import (
	"regexp"
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

	description = `成為這個頻道的會員並獲得獎勵：
https://www.youtube.com/channel/UCOQFrooz-YGHjYb7s3-MrsQ/join
_____________________________________________
想聽我既音樂作品可以去下面LINK
streetvoice 街聲：
https://streetvoice.com/CTLam331/
_____________________________________________
想學結他、鋼琴
有關音樂制作工作
都可以搵我～
大家快D訂閱喇
不定期出片




Website: http://ctlam331.wixsite.com/ctlamusic
FB PAGE：https://www.facebook.com/ctlam331
IG：ctlamusic`
	urlsRegex := regexp.MustCompile(`(?m) ?(f|ht)(tp)(s?)(://)(.*)[.|/](.*)`)
	descriptionSample := urlsRegex.ReplaceAllString(description, "")
	info = whatlanggo.Detect(descriptionSample)
	logrus.Infof("confidence: %.2f", info.Confidence)
	assert.True(t, info.IsReliable())
	assert.True(t, info.Lang.Iso6391() != "")
	assert.Equal(t, "zh", info.Lang.Iso6391())
}
