package dnsnameresolvercrd

import (
	"context"
	"sync"

	"github.com/openshift/coredns-ocp-dnsnameresolver/operator/pkg/manifests"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	controllerName = "dnsnameresolver_crd_controller"
)

var (
	controllerLog = ctrl.Log.WithName(controllerName)
)

// New creates and returns a controller that creates DNSNameResolver.
func New(mgr manager.Manager, config Config) (controller.Controller, error) {
	reconciler := &reconciler{
		cache:  mgr.GetCache(),
		client: mgr.GetClient(),
		config: config,
	}
	c, err := controller.New(controllerName, mgr, controller.Options{Reconciler: reconciler})
	if err != nil {
		return nil, err
	}
	clusterNamePredicate := predicate.NewPredicateFuncs(func(o client.Object) bool {
		return manifests.DNSNameResolverCRD().Name == o.GetName()
	})
	if err := c.Watch(source.Kind(mgr.GetCache(), &apiextensionsv1.CustomResourceDefinition{}),
		&handler.EnqueueRequestForObject{}, clusterNamePredicate); err != nil {
		return nil, err
	}

	return c, nil
}

// Config holds all the configuration that must be provided when creating the
// controller.
type Config struct {
	// DependentCaches is a list of caches that are used by Controllers watching DNSNameResolver
	// resources. The dnsnameresolver_crd controller starts these caches once
	// the DNSNameResolver CRD has been created.
	DependentCaches []cache.Cache
	// DependentControllers is a list of controllers that watch DNSNameResolver
	// resources. The dnsnameresolver_crd controller starts these controllers once
	// the DNSNameResolver CRD has been created and the DependentCaches are started.
	DependentControllers []controller.Controller
}

// reconciler handles the actual CRD reconciliation logic in response to
// events.
type reconciler struct {
	config Config

	cache            cache.Cache
	client           client.Client
	startCaches      sync.Once
	startControllers sync.Once
}

// Reconcile expects request to refer to a CRD and creates or
// reconciles the DNSNameResolver CRD.
func (r *reconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	controllerLog.Info("reconciling", "request", request)

	// Ensure the DNSNameResolver CRD is created.
	if err := r.ensureDNSNameResolverCRD(ctx); err != nil {
		return reconcile.Result{}, err
	}

	// Start the dependent caches and wait for the caches to sync.
	r.startCaches.Do(func() {
		var wg sync.WaitGroup
		for i := range r.config.DependentCaches {
			cache := &r.config.DependentCaches[i]

			// Start the dependent cache.
			go func() {
				if err := (*cache).Start(ctx); err != nil {
					controllerLog.Error(err, "cannot start cache")
				}
			}()

			// Wait for the dependent cache to sync.
			wg.Add(1)
			go func() {
				defer wg.Done()
				if started := (*cache).WaitForCacheSync(ctx); !started {
					controllerLog.Info("failed to sync cache before starting controllers")
				}
			}()
		}
		// Wait for all the dependent caches to sync.
		wg.Wait()

		controllerLog.Info("dependent caches synced")
	})

	// Start the dependent controllers after the dependent caches have synced.
	r.startControllers.Do(func() {
		for i := range r.config.DependentControllers {
			controller := &r.config.DependentControllers[i]

			// Start the dependent controller.
			go func() {
				if err := (*controller).Start(ctx); err != nil {
					controllerLog.Error(err, "cannot start controller")
				}
			}()
		}

		controllerLog.Info("dependent controllers started")
	})

	return reconcile.Result{}, nil
}
