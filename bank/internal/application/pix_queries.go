package application

import (
	"context"

	"centavo/bank/internal/domain/transfer"
)

type ExternalTransferReader interface {
	FindExternalTransfer(ctx context.Context, id transfer.ID) (ExternalTransfer, []TransitionRecord, error)
}

type ExternalTransferView struct {
	Transfer    ExternalTransfer
	Transitions []TransitionRecord
}

type GetExternalTransfer struct {
	reader ExternalTransferReader
}

func NewGetExternalTransfer(reader ExternalTransferReader) GetExternalTransfer {
	return GetExternalTransfer{reader: reader}
}

func (g GetExternalTransfer) Execute(ctx context.Context, id transfer.ID) (ExternalTransferView, error) {
	external, transitions, err := g.reader.FindExternalTransfer(ctx, id)
	if err != nil {
		return ExternalTransferView{}, err
	}

	return ExternalTransferView{Transfer: external, Transitions: transitions}, nil
}
