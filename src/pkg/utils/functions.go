package utils

import (
	"text/template"

	"github.com/Masterminds/sprig"
	"github.com/bililive-go/bililive-go/src/configs"
)

func getFunctionList(config *configs.Config) map[string]interface{} {
	filenameFilters := []StringFilter{
		UnescapeHTMLEntity,
		ReplaceIllegalChar,
	}
	if config.Feature.RemoveSymbolOtherCharacter {
		filenameFilters = append(filenameFilters, RemoveSymbolOtherChar)
	}
	return map[string]interface{}{
		"decodeUnicode":      ParseUnicode,
		"replaceIllegalChar": ReplaceIllegalChar,
		"unescapeHTMLEntity": UnescapeHTMLEntity,
		"filenameFilter":     NewStringFilterChain(filenameFilters...).Do,
	}
}

func GetFuncMap(config *configs.Config) template.FuncMap {
	funcs := sprig.TxtFuncMap()
	for key, fn := range getFunctionList(config) {
		funcs[key] = fn
	}
	return funcs
}
