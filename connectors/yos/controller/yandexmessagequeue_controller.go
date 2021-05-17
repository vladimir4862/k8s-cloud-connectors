// Copyright (c) 2021 Yandex LLC. All rights reserved.
// Author: Martynov Pavel <covariance@yandex-team.ru>

package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	connectorsv1 "k8s-connectors/connectors/yos/api/v1"
	"k8s-connectors/connectors/yos/controller/adapter"
	"k8s-connectors/connectors/yos/controller/phase"
	yosconfig "k8s-connectors/connectors/yos/pkg/config"
	"k8s-connectors/pkg/config"
	"k8s-connectors/pkg/util"
)

// yandexObjectStorageReconciler reconciles a YandexContainerRegistry object
type yandexObjectStorageReconciler struct {
	client.Client
	log logr.Logger
	// phases that are to be invoked on this object
	// IsUpdated blocks Update, and order of initializers matters,
	// thus if one of initializers fails, subsequent won't be processed.
	// Upon destruction of object, phase cleanups are called in
	// reverse order.
	phases []phase.YandexObjectStoragePhase
}

func NewYandexObjectStorageReconciler(
	cl client.Client, log logr.Logger,
) (*yandexObjectStorageReconciler, error) {
	sdk, err := adapter.NewYandexObjectStorageAdapterSDK()
	if err != nil {
		return nil, err
	}
	return &yandexObjectStorageReconciler{
		Client: cl,
		log:    log,
		phases: []phase.YandexObjectStoragePhase{
			&phase.FinalizerRegistrar{
				Client: cl,
			},
			&phase.ResourceAllocator{
				Client: cl,
				Sdk:    sdk,
			},
		},
	}, nil
}

// +kubebuilder:rbac:groups=connectors.cloud.yandex.com,resources=yandexobjectstorages,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=connectors.cloud.yandex.com,resources=yandexobjectstorages/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=connectors.cloud.yandex.com,resources=yandexobjectstorages/finalizers,verbs=update
// +kubebuilder:rbac:groups=connectors.cloud.yandex.com,resources=staticaccesskeys,verbs=get
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

func (r *yandexObjectStorageReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.log.WithValues(yosconfig.LongName, req.NamespacedName)

	// Try to retrieve resource from k8s
	var resource connectorsv1.YandexObjectStorage
	if err := r.Get(ctx, req.NamespacedName, &resource); err != nil {
		// It still can be OK if we have not found it, and we do not need to reconcile it again

		// This outcome signifies that we just cannot find resource, that is ok
		if apierrors.IsNotFound(err) {
			log.Info("Resource not found in k8s, reconciliation not possible")
			return config.GetNeverResult()
		}

		// Some unexpected error occurred, must throw
		return config.GetErroredResult(err)
	}

	// If object must be currently finalized, do it and quit
	mustBeFinalized, err := r.mustBeFinalized(&resource)
	if err != nil {
		return config.GetErroredResult(err)
	}
	if mustBeFinalized {
		if err := r.finalize(ctx, log, &resource); err != nil {
			return config.GetErroredResult(err)
		}
		return config.GetNormalResult()
	}

	// Update all fragments of object, keeping track of whether
	// all of them are initialized
	for _, updater := range r.phases {
		isInitialized, err := updater.IsUpdated(ctx, &resource)
		if err != nil {
			return config.GetErroredResult(err)
		}
		if !isInitialized {
			if err := updater.Update(ctx, log, &resource); err != nil {
				return config.GetErroredResult(err)
			}
		}
	}

	return config.GetNormalResult()
}

func (r *yandexObjectStorageReconciler) mustBeFinalized(registry *connectorsv1.YandexObjectStorage) (bool, error) {
	return !registry.DeletionTimestamp.IsZero() && util.ContainsString(
		registry.Finalizers, yosconfig.FinalizerName,
	), nil
}

func (r *yandexObjectStorageReconciler) finalize(
	ctx context.Context, log logr.Logger, registry *connectorsv1.YandexObjectStorage,
) error {
	for i := len(r.phases); i != 0; i-- {
		if err := r.phases[i-1].Cleanup(ctx, log, registry); err != nil {
			return fmt.Errorf("error during finalization: %v", err)
		}
	}
	log.Info("resource finalized successfully")
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *yandexObjectStorageReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&connectorsv1.YandexObjectStorage{}).
		Complete(r)
}
