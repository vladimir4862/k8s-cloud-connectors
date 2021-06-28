// Copyright (c) 2021 Yandex LLC. All rights reserved.
// Author: Martynov Pavel <covariance@yandex-team.ru>

package webhook

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type validatingHandler struct {
	object    runtime.Object
	decoder   *admission.Decoder
	log       logr.Logger
	validator Validator
}

func NewValidatingHandler(m Validator) admission.Handler {
	return &validatingHandler{
		log:       logr.Discard(),
		validator: m,
	}
}

func (r *validatingHandler) InjectObject(obj runtime.Object) error {
	r.object = obj
	return nil
}

func (r *validatingHandler) InjectDecoder(decoder *admission.Decoder) error {
	r.decoder = decoder
	return nil
}

func (r *validatingHandler) InjectLogger(log logr.Logger) error {
	r.log = log
	return nil
}

func (r *validatingHandler) Handle(ctx context.Context, req admission.Request) admission.Response { //nolint:gocritic
	// GoCritic warns about `hugeParam` req, but it is an interface that we are obliged to follow
	obj := r.object.DeepCopyObject()

	// It is `exhaustive` because of default case, but linter seems to raise false positive error here
	switch req.Operation { //nolint:exhaustive
	case v1.Create:
		if err := r.decoder.Decode(req, obj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		return handleValidationError(r.validator.ValidateCreation(ctx, r.log, obj))
	case v1.Update:
		old := r.object.DeepCopyObject()
		if err := r.decoder.DecodeRaw(req.Object, obj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		if err := r.decoder.DecodeRaw(req.OldObject, old); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		return handleValidationError(r.validator.ValidateUpdate(ctx, r.log, obj, old))
	case v1.Delete:
		if err := r.decoder.DecodeRaw(req.OldObject, obj); err != nil {
			return admission.Errored(http.StatusBadRequest, err)
		}
		return handleValidationError(r.validator.ValidateDeletion(ctx, r.log, obj))
	default:
		return admission.Errored(http.StatusBadRequest, fmt.Errorf("invalid request operation: %s", req.Operation))
	}
}

func handleValidationError(err error) admission.Response {
	if err != nil {
		if errors.Is(err, ValidationError{}) {
			return admission.Denied(err.Error())
		}
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.Allowed("")
}
