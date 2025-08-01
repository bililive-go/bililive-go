package utils

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strconv"
)

type StringFilter interface {
	Do(string) string
}

type StringFilterFunc func(string) string

func (f StringFilterFunc) Do(s string) string {
	return f(s)
}

type StringFilterChain struct {
	filters []StringFilter
}

func NewStringFilterChain(filter ...StringFilter) *StringFilterChain {
	return &StringFilterChain{
		filters: filter,
	}
}

func (c *StringFilterChain) Do(str string) string {
	for _, f := range c.filters {
		str = f.Do(str)
	}
	return str
}

func ParseString(str string, filter ...StringFilter) string {
	return NewStringFilterChain(filter...).Do(str)
}

var ParseUnicode = StringFilterFunc(func(str string) string {
	buf := new(bytes.Buffer)
	chars := []byte(str)
	for i := 0; i < len(str); {
		if chars[i] == 92 && chars[i+1] == 117 {
			t, _ := strconv.ParseInt(string(chars[i+2:i+6]), 16, 32)
			fmt.Fprintf(buf, "%c", t)
			i += 6
		} else {
			buf.WriteByte(chars[i])
			i++
		}
	}
	return buf.String()
})

var ReplaceIllegalChar = StringFilterFunc(func(str string) string {
	reg := regexp.MustCompile(`[\/\\\:\*\?\"\<\>\|]|[\.\s]+$`)
	for reg.MatchString(str) {
		str = reg.ReplaceAllString(str, "_")
	}
	return str
})

var UnescapeHTMLEntity = StringFilterFunc(html.UnescapeString)

var RemoveSymbolOtherChar = StringFilterFunc(func(str string) string {
	reg := regexp.MustCompile(`\p{So}`)
	for reg.MatchString(str) {
		str = reg.ReplaceAllString(str, "_")
	}
	return str
})
