package grok

import (
	"encoding/base64"
	"fmt"
	"math/rand"
)

const fixedStatsigID = "ZTpUeXBlRXJyb3I6IENhbm5vdCByZWFkIHByb3BlcnRpZXMgb2YgdW5kZWZpbmVkIChyZWFkaW5nICdjaGlsZE5vZGVzJyk="

func GenStatsigID(dynamic bool) string {
	if !dynamic {
		return fixedStatsigID
	}
	var raw string
	if rand.Intn(2) == 0 {
		r := randomAlphaNum(5)
		raw = fmt.Sprintf("e:TypeError: Cannot read properties of null (reading 'children['%s']')", r)
	} else {
		prop := randomAlpha(10)
		raw = fmt.Sprintf("e:TypeError: Cannot read properties of undefined (reading '%s')", prop)
	}
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

func randomAlphaNum(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func randomAlpha(n int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, n)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}
