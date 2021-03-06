package cis

import (
	"context"

	"github.com/rancher/rancher/pkg/systemaccount"
	"github.com/rancher/types/config"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Register initializes the controllers and registers
func Register(ctx context.Context, userContext *config.UserContext) {
	logrus.Infof("Registering CIS controller")

	clusterName := userContext.ClusterName
	clusterLister := userContext.Management.Management.Clusters(metav1.NamespaceAll).Controller().Lister()
	projectLister := userContext.Management.Management.Projects(metav1.NamespaceAll).Controller().Lister()

	mgmtContext := userContext.Management

	userNSClient := userContext.Core.Namespaces(metav1.NamespaceAll)
	mgmtProjClient := mgmtContext.Management.Projects(clusterName)
	mgmtAppClient := mgmtContext.Project.Apps(metav1.NamespaceAll)
	mgmtTemplateVersionLister := mgmtContext.Management.CatalogTemplateVersions(metav1.NamespaceAll).Controller().Lister()
	systemAccountManager := systemaccount.NewManager(mgmtContext)

	mgmtClusterClient := mgmtContext.Management.Clusters(metav1.NamespaceAll)
	mgmtClusterScanClient := mgmtContext.Management.ClusterScans(clusterName)
	pods := userContext.Core.Pods(DefaultNamespaceForCis)
	configMapsClient := userContext.Core.ConfigMaps(DefaultNamespaceForCis)

	podHandler := &podHandler{
		mgmtClusterScanClient,
		mgmtClusterClient,
		clusterLister,
		userContext.ClusterName,
	}

	clusterScanHandler := &cisScanHandler{
		mgmtCtxClusterClient:         mgmtClusterClient,
		mgmtCtxProjClient:            mgmtProjClient,
		mgmtCtxAppClient:             mgmtAppClient,
		mgmtCtxTemplateVersionLister: mgmtTemplateVersionLister,
		mgmtCtxClusterScanClient:     mgmtClusterScanClient,
		systemAccountManager:         systemAccountManager,
		userCtx:                      userContext,
		clusterNamespace:             userContext.ClusterName,
		userCtxNSClient:              userNSClient,
		clusterLister:                clusterLister,
		projectLister:                projectLister,
		configMapsClient:             configMapsClient,
	}

	pods.AddHandler(ctx, "podHandler", podHandler.Sync)
	mgmtClusterScanClient.AddClusterScopedLifecycle(ctx, "cisScanHandler", clusterName, clusterScanHandler)
}
