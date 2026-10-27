package application

import (
	"context"

	"centavo/bank/internal/domain/idempotency"
)

type Resolution struct {
	Outcome  idempotency.Outcome
	Response *idempotency.StoredResponse
}

// Its key and what it sent, since a key can end up holding another request.
type ResolveRequest struct {
	Key         idempotency.Key
	Endpoint    string
	RequestHash idempotency.RequestHash
}

type Resolver interface {
	Resolve(ctx context.Context, request ResolveRequest) (Resolution, error)
}

// TombstoneResolver is idempotency's resolve: a missing key gets a tombstone,
// so a late original request can never apply after the answer "voided".
type TombstoneResolver struct {
	unitOfWork UnitOfWork
}

func NewTombstoneResolver(unitOfWork UnitOfWork) TombstoneResolver {
	return TombstoneResolver{unitOfWork: unitOfWork}
}

func (r TombstoneResolver) Resolve(ctx context.Context, request ResolveRequest) (Resolution, error) {
	var resolution Resolution

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		resolution, err = resolveKey(ctx, stores, request)

		return err
	})

	return resolution, err
}

func resolveKey(ctx context.Context, stores KeyStore, request ResolveRequest) (Resolution, error) {
	existing, isVoided, err := stores.VoidKey(ctx, request.Key)
	if err != nil {
		return Resolution{}, err
	}

	if isVoided {
		return Resolution{Outcome: idempotency.OutcomeVoided}, nil
	}

	if existing.Status == idempotency.StatusPending {
		return Resolution{}, idempotency.ErrRequestInProgress
	}

	err = stores.TouchKey(ctx, request.Key)
	if err != nil {
		return Resolution{}, err
	}

	if existing.Status == idempotency.StatusVoided {
		return Resolution{Outcome: idempotency.OutcomeVoided}, nil
	}

	// The same check a retry gets: the stored answer belongs to the request
	// that took the key, and only that request may hear "applied".
	isSameRequest := existing.Endpoint == request.Endpoint && existing.RequestHash == request.RequestHash
	if !isSameRequest {
		return Resolution{Outcome: idempotency.OutcomeSuperseded}, nil
	}

	response := existing.Response

	return Resolution{Outcome: idempotency.OutcomeApplied, Response: &response}, nil
}
