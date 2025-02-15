package jindo

import (
	"context"
	"fmt"
	"reflect"

	"github.com/fluid-cloudnative/fluid/pkg/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	datav1alpha1 "github.com/fluid-cloudnative/fluid/api/v1alpha1"
	"github.com/fluid-cloudnative/fluid/pkg/utils"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/retry"
)

// SetupWorkers checks the desired and current replicas of workers and makes an update
// over the status by setting phases and conditions. The function
// calls for a status update and finally returns error if anything unexpected happens.
func (e *JindoEngine) SetupWorkers() (err error) {
	runtime, err := e.getRuntime()
	if err != nil {
		e.Log.Error(err, "setupWorker")
		return err
	}

	replicas := runtime.Replicas()

	currentReplicas, err := e.AssignNodesToCache(replicas)
	if err != nil {
		return err
	}

	e.Log.Info("check the desired and current replicas",
		"desiredReplicas", replicas,
		"currentReplicas", currentReplicas)

	if currentReplicas == 0 {
		return fmt.Errorf("the number of the current workers which can be scheduled is 0")
	}

	// 2. Update the status
	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		runtime, err := e.getRuntime()
		if err != nil {
			e.Log.Error(err, "setupWorker")
			return err
		}

		runtimeToUpdate := runtime.DeepCopy()

		runtimeToUpdate.Status.WorkerPhase = datav1alpha1.RuntimePhaseNotReady
		runtimeToUpdate.Status.DesiredWorkerNumberScheduled = replicas
		runtimeToUpdate.Status.CurrentWorkerNumberScheduled = currentReplicas

		if len(runtimeToUpdate.Status.Conditions) == 0 {
			runtimeToUpdate.Status.Conditions = []datav1alpha1.RuntimeCondition{}
		}
		cond := utils.NewRuntimeCondition(datav1alpha1.RuntimeWorkersInitialized, datav1alpha1.RuntimeWorkersInitializedReason,
			"The workers are initialized.", corev1.ConditionTrue)
		runtimeToUpdate.Status.Conditions =
			utils.UpdateRuntimeCondition(runtimeToUpdate.Status.Conditions,
				cond)

		if !reflect.DeepEqual(runtime.Status, runtimeToUpdate.Status) {
			return e.Client.Status().Update(context.TODO(), runtimeToUpdate)
		}

		return nil
	})

	return
}

// ShouldSetupWorkers checks if we need setup the workers
func (e *JindoEngine) ShouldSetupWorkers() (should bool, err error) {
	runtime, err := e.getRuntime()
	if err != nil {
		return
	}

	switch runtime.Status.WorkerPhase {
	case datav1alpha1.RuntimePhaseNone:
		should = true
	default:
		should = false
	}

	return
}

// are the workers ready
func (e *JindoEngine) CheckWorkersReady() (ready bool, err error) {
	var (
		workerReady, workerPartialReady bool
		workerName                      string = e.getWorkerDaemonsetName()
		namespace                       string = e.namespace
	)

	runtime, err := e.getRuntime()
	if err != nil {
		return ready, err
	}

	workers, err := e.getDaemonset(workerName, namespace)
	if err != nil {
		return ready, err
	}

	if workers.Status.NumberReady > 0 {
		if runtime.Replicas() == workers.Status.NumberReady {
			workerReady = true
		} else if workers.Status.NumberReady >= 1 {
			workerPartialReady = true
		}
	}

	if workerReady || workerPartialReady {
		ready = true
	} else {
		e.Log.Info("workers are not ready", "workerReady", workerReady,
			"workerPartialReady", workerPartialReady)
		return
	}

	// update the status as the workers are ready
	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		runtime, err := e.getRuntime()
		if err != nil {
			return err
		}
		runtimeToUpdate := runtime.DeepCopy()
		if len(runtimeToUpdate.Status.Conditions) == 0 {
			runtimeToUpdate.Status.Conditions = []datav1alpha1.RuntimeCondition{}
		}
		cond := utils.NewRuntimeCondition(datav1alpha1.RuntimeWorkersReady, datav1alpha1.RuntimeWorkersReadyReason,
			"The workers are ready.", corev1.ConditionTrue)
		if workerPartialReady {
			cond = utils.NewRuntimeCondition(datav1alpha1.RuntimeWorkersReady, datav1alpha1.RuntimeWorkersReadyReason,
				"The workers are partially ready.", corev1.ConditionTrue)

			runtimeToUpdate.Status.WorkerPhase = datav1alpha1.RuntimePhasePartialReady
		}
		runtimeToUpdate.Status.Conditions =
			utils.UpdateRuntimeCondition(runtimeToUpdate.Status.Conditions,
				cond)

		if !reflect.DeepEqual(runtime.Status, runtimeToUpdate.Status) {
			return e.Client.Status().Update(context.TODO(), runtimeToUpdate)
		}

		return nil
	})

	return
}

// getWorkerSelectors gets the selector of the worker
func (e *JindoEngine) getWorkerSelectors() string {
	labels := map[string]string{
		"release":     e.name,
		POD_ROLE_TYPE: WOKRER_POD_ROLE,
		"app":         common.JINDO_RUNTIME,
	}
	labelSelector := &metav1.LabelSelector{
		MatchLabels: labels,
	}

	selectorValue := ""
	selector, err := metav1.LabelSelectorAsSelector(labelSelector)
	if err != nil {
		e.Log.Error(err, "Failed to parse the labelSelector of the runtime", "labels", labels)
	} else {
		selectorValue = selector.String()
	}
	return selectorValue
}
