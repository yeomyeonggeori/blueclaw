package harnessselection

import (
	"github.com/yeomyeonggeori/blueprotocol/toolcontract"
)

func emptyToolSet() *toolcontract.ToolSet {
	return toolcontract.NewToolSet(nil)
}
