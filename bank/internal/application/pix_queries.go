package application

import (
	"context"

	"centavo/bank/internal/domain/transfer"
)

// ExternalTransferReader reads a transfer and its history.
type ExternalTransferReader interface {
	FindExternalTransfer(ctx context.Context, id transfer.ID) (ExternalTransfer, []TransitionRecord, error)
}

// ExternalTransferView is a transfer with its state history.
type ExternalTransferView struct {
	Transfer    ExternalTransfer
	Transitions []TransitionRecord
}

// GetExternalTransfer reads one PIX out.
type GetExternalTransfer struct {
	reader ExternalTransferReader
}

// NewGetExternalTransfer builds the query.
func NewGetExternalTransfer(reader ExternalTransferReader) GetExternalTransfer {
	return GetExternalTransfer{reader: reader}
}

// Execute returns transfer.ErrTransferNotFound for an unknown id.
func (g GetExternalTransfer) Execute(ctx context.Context, id transfer.ID) (ExternalTransferView, error) {
	external, transitions, err := g.reader.FindExternalTransfer(ctx, id)
	if err != nil {
		return ExternalTransferView{}, err
	}

	return ExternalTransferView{Transfer: external, Transitions: transitions}, nil
}
