package llm

import (
	"context"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

type reportedWireExchange struct {
	WireExchange *model.WireExchange `json:"wireExchange,omitempty"`
}

func (reported reportedWireExchange) record(ctx context.Context) {
	if reported.WireExchange == nil {
		return
	}
	model.RecordWireExchange(ctx, *reported.WireExchange)
}
