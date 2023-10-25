package aws_ebs

import (
	"fmt"
	"path/filepath"
	"strings"

	opv1 "github.com/openshift/api/operator/v1"
	"github.com/openshift/csi-operator/assets"
	"github.com/openshift/csi-operator/pkg/clients"
	"github.com/openshift/csi-operator/pkg/generator"
	common "github.com/openshift/csi-operator/pkg/generator/common"
	"github.com/openshift/csi-operator/pkg/operator/config"
	"github.com/openshift/csi-operator/pkg/operator/config/defaults"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/csi/csidrivercontrollerservicecontroller"
	"github.com/openshift/library-go/pkg/operator/csi/csidrivernodeservicecontroller"
	"github.com/openshift/library-go/pkg/operator/csi/csistorageclasscontroller"
	dc "github.com/openshift/library-go/pkg/operator/deploymentcontroller"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
)

const (
	cloudCredSecretName   = "ebs-cloud-credentials"
	metricsCertSecretName = "aws-ebs-csi-driver-controller-metrics-serving-cert"
	infrastructureName    = "cluster"
	cloudConfigNamespace  = "openshift-config-managed"
	cloudConfigName       = "kube-cloud-config"
	caBundleKey           = "ca-bundle.pem"
	trustedCAConfigMap    = "aws-ebs-csi-driver-trusted-ca-bundle"
	kmsKeyID              = "kmsKeyId"

	assetBase = "overlays/aws-ebs"
)

// GetAWSEBSGeneratorConfig returns configuration for generating assets of  AWS EBS CSI driver operator.
func GetAWSEBSGeneratorConfig() *generator.CSIDriverGeneratorConfig {
	return &generator.CSIDriverGeneratorConfig{
		AssetPrefix:      "aws-ebs-csi-driver",
		AssetShortPrefix: "ebs",
		DriverName:       "ebs.csi.aws.com",
		OutputDir:        filepath.Join(assetBase, "generated"),

		ControllerConfig: &generator.ControlPlaneConfig{
			DeploymentTemplateAssetName: filepath.Join(assetBase, "patches/controller_add_driver.yaml"),
			LivenessProbePort:           10301,
			MetricsPorts: []generator.MetricsPort{
				{
					LocalPort:           common.AWSEBSLoopbackMetricsPortStart,
					InjectKubeRBACProxy: true,
					ExposedPort:         common.AWSEBSExposedMetricsPortStart,
					Name:                "driver-m",
				},
			},
			SidecarLocalMetricsPortStart:   common.AWSEBSLoopbackMetricsPortStart + 1,
			SidecarExposedMetricsPortStart: common.AWSEBSExposedMetricsPortStart + 1,
			Sidecars: []generator.SidecarConfig{
				common.DefaultProvisionerWithSnapshots.WithExtraArguments(
					"--default-fstype=ext4",
					"--feature-gates=Topology=true",
					"--extra-create-metadata=true",
					"--timeout=60s",
				),
				common.DefaultAttacher.WithExtraArguments(
					"--timeout=60s",
				),
				common.DefaultResizer.WithExtraArguments(
					"--timeout=300s",
				),
				common.DefaultSnapshotter.WithExtraArguments(
					"--timeout=300s",
					"--extra-create-metadata",
				),
				common.DefaultLivenessProbe.WithExtraArguments(
					"--probe-timeout=3s",
				),
			},
			Assets: common.DefaultControllerAssets,
			AssetPatches: common.DefaultAssetPatches.WithPatches(generator.HyperShiftOnly,
				"controller.yaml", filepath.Join(assetBase, "patches/controller_add_hypershift_controller_minter.yaml"),
			),
		},

		GuestConfig: &generator.GuestConfig{
			DaemonSetTemplateAssetName: filepath.Join(assetBase, "patches/node_add_driver.yaml"),
			LivenessProbePort:          10300,
			Sidecars: []generator.SidecarConfig{
				common.DefaultNodeDriverRegistrar,
				common.DefaultLivenessProbe.WithExtraArguments(
					"--probe-timeout=3s",
				),
			},
			Assets: common.DefaultNodeAssets.WithAssets(generator.AllFlavours,
				filepath.Join(assetBase, "base/csidriver.yaml"),
				filepath.Join(assetBase, "base/storageclass_gp2.yaml"),
				filepath.Join(assetBase, "base/storageclass_gp3.yaml"),
				filepath.Join(assetBase, "base/volumesnapshotclass.yaml"),
			),
		},
	}
}

// GetAWSEBSOperatorConfig returns runtime configuration of the CSI driver operator.
func GetAWSEBSOperatorConfig() *config.OperatorConfig {
	return &config.OperatorConfig{
		CSIDriverName:                   opv1.AWSEBSCSIDriver,
		UserAgent:                       "aws-ebs-csi-driver-operator",
		AssetReader:                     assets.ReadFile,
		AssetDir:                        filepath.Join(assetBase, "generated"),
		OperatorControllerConfigBuilder: GetAWSEBSOperatorControllerConfig,
	}
}

// GetAWSEBSOperatorControllerConfig returns second half of runtime configuration of the CSI driver operator,
// after a client connection + cluster flavour are established.
func GetAWSEBSOperatorControllerConfig(flavour generator.ClusterFlavour, c *clients.Clients) *config.OperatorControllerConfig {
	cfg := defaults.NewDefaultOperatorControllerConfig(flavour, c, "AWSEBS")

	// Hooks to run on all clusters
	cfg.AddDeploymentHookBuilders(c,
		withAWSRegion,
		withCustomTags,
		withCustomEndPoint,
		withCABundleDeploymentHook)
	cfg.AddDaemonSetHookBuilders(c, withCABundleDaemonSetHook)
	cfg.AddStorageClassHookBuilders(c, withKMSKeyHook)

	if flavour == generator.FlavourStandalone {
		// Standalone-only hooks
		cfg.AddDeploymentHookBuilders(c, getCustomAWSCABundleBuilder(cloudConfigName))
	} else {
		// HyperShift only hooks
		cfg.AddDeploymentHookBuilders(c, getCustomAWSCABundleBuilder("user-ca-bundle"))
	}
	cfg.DeploymentWatchedSecretNames = append(cfg.DeploymentWatchedSecretNames, cloudCredSecretName, metricsCertSecretName)

	// extra controllers
	if flavour == generator.FlavourStandalone {
		ctrl, err := newCustomAWSBundleSyncer(c)
		if err != nil {
			panic(err)
		}
		cfg.ExtraControlPlaneControllers = append(cfg.ExtraControlPlaneControllers, ctrl)
	}

	return cfg
}

// getCustomAWSCABundleBuilder executes the asset as a template to fill out the parts required when using a custom CA bundle.
// The `caBundleConfigMap` parameter specifies the name of the ConfigMap containing the custom CA bundle. If the
// argument supplied is empty, then no custom CA bundle will be used.
func getCustomAWSCABundleBuilder(cmName string) config.DeploymentHookBuilder {
	return func(c *clients.Clients) (dc.DeploymentHookFunc, []factory.Informer) {
		hook := func(_ *opv1.OperatorSpec, deployment *appsv1.Deployment) error {
			cloudConfigLister := c.GetControlPlaneConfigMapInformer(c.ControlPlaneNamespace).Lister().ConfigMaps(c.ControlPlaneNamespace)
			configName, err := customAWSCABundle(cmName, cloudConfigLister)
			if err != nil {
				return fmt.Errorf("could not determine if a custom CA bundle is in use: %w", err)
			}
			if configName == "" {
				return nil
			}

			deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
				Name: "ca-bundle",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: configName},
					},
				},
			})
			for i := range deployment.Spec.Template.Spec.Containers {
				container := &deployment.Spec.Template.Spec.Containers[i]
				if container.Name != "csi-driver" {
					continue
				}
				container.Env = append(container.Env, corev1.EnvVar{
					Name:  "AWS_CA_BUNDLE",
					Value: "/etc/ca/ca-bundle.pem",
				})
				container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
					Name:      "ca-bundle",
					MountPath: "/etc/ca",
					ReadOnly:  true,
				})
				return nil
			}
			return fmt.Errorf("could not use custom CA bundle because the csi-driver container is missing from the deployment")
		}
		informers := []factory.Informer{
			c.GetControlPlaneConfigMapInformer(c.ControlPlaneNamespace).Informer(),
		}
		return hook, informers
	}
}

// withCustomEndPoint sets driver's AWS_EC2_ENDPOINT env. var. from
// infrastructure.Status.PlatformStatus.AWS.ServiceEndpoints.
func withCustomEndPoint(c *clients.Clients) (dc.DeploymentHookFunc, []factory.Informer) {
	hook := func(_ *opv1.OperatorSpec, deployment *appsv1.Deployment) error {
		infraLister := c.GetGuestInfraInformer().Lister()
		infra, err := infraLister.Get(infrastructureName)
		if err != nil {
			return err
		}
		if infra.Status.PlatformStatus == nil || infra.Status.PlatformStatus.AWS == nil {
			return nil
		}
		serviceEndPoints := infra.Status.PlatformStatus.AWS.ServiceEndpoints
		ec2EndPoint := ""
		for _, serviceEndPoint := range serviceEndPoints {
			if serviceEndPoint.Name == "ec2" {
				ec2EndPoint = serviceEndPoint.URL
			}
		}
		if ec2EndPoint == "" {
			return nil
		}

		for i := range deployment.Spec.Template.Spec.Containers {
			container := &deployment.Spec.Template.Spec.Containers[i]
			if container.Name != "csi-driver" {
				continue
			}
			container.Env = append(container.Env, corev1.EnvVar{
				Name:  "AWS_EC2_ENDPOINT",
				Value: ec2EndPoint,
			})
			return nil
		}
		return nil
	}
	informers := []factory.Informer{
		c.GetGuestInfraInformer().Informer(),
	}
	return hook, informers
}

// newCustomAWSBundleSyncer creates a controller that syncs the custom CA bundle ConfigMap to control plane namespace,
// so it can be projected into the CSI driver containers.
func newCustomAWSBundleSyncer(c *clients.Clients) (factory.Controller, error) {
	// sync config map with additional trust bundle to the operator namespace,
	// so the operator can get it as a ConfigMap volume.
	srcConfigMap := resourcesynccontroller.ResourceLocation{
		Namespace: cloudConfigNamespace,
		Name:      cloudConfigName,
	}
	dstConfigMap := resourcesynccontroller.ResourceLocation{
		Namespace: clients.CSIDriverNamespace,
		Name:      cloudConfigName,
	}
	certController := resourcesynccontroller.NewResourceSyncController(
		c.OperatorClient,
		c.GuestKubeInformers,
		c.GuestKubeClient.CoreV1(),
		c.GuestKubeClient.CoreV1(),
		c.EventRecorder)
	err := certController.SyncConfigMap(dstConfigMap, srcConfigMap)
	if err != nil {
		return nil, err
	}
	return certController, nil
}

// customAWSCABundle returns true if the cloud config ConfigMap exists and contains a custom CA bundle.
func customAWSCABundle(configName string, cloudConfigLister corev1listers.ConfigMapNamespaceLister) (string, error) {
	cloudConfigCM, err := cloudConfigLister.Get(configName)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get the %s ConfigMap: %w", configName, err)
	}

	if _, ok := cloudConfigCM.Data[caBundleKey]; !ok {
		return "", nil
	}
	return configName, nil
}

// withCABundleDeploymentHook projects custom CA bundle ConfigMap into the CSI driver container
func withCABundleDeploymentHook(c *clients.Clients) (dc.DeploymentHookFunc, []factory.Informer) {
	hook := csidrivercontrollerservicecontroller.WithCABundleDeploymentHook(
		c.ControlPlaneNamespace,
		trustedCAConfigMap,
		c.GetControlPlaneConfigMapInformer(c.ControlPlaneNamespace),
	)
	informers := []factory.Informer{
		c.GetControlPlaneConfigMapInformer(c.ControlPlaneNamespace).Informer(),
	}
	return hook, informers
}

// withCABundleDaemonSetHook projects custom CA bundle ConfigMap into the CSI driver container
func withCABundleDaemonSetHook(c *clients.Clients) (csidrivernodeservicecontroller.DaemonSetHookFunc, []factory.Informer) {
	hook := csidrivernodeservicecontroller.WithCABundleDaemonSetHook(
		clients.CSIDriverNamespace,
		trustedCAConfigMap,
		c.GetGuestConfigMapInformer(clients.CSIDriverNamespace),
	)
	informers := []factory.Informer{
		c.GetGuestConfigMapInformer(clients.CSIDriverNamespace).Informer(),
	}
	return hook, informers
}

// withCustomTags add tags from Infrastructure.Status.PlatformStatus.AWS.ResourceTags to the driver command line as
// --extra-tags=<key1>=<value1>,<key2>=<value2>,...
func withCustomTags(c *clients.Clients) (dc.DeploymentHookFunc, []factory.Informer) {
	hook := func(spec *opv1.OperatorSpec, deployment *appsv1.Deployment) error {
		infraLister := c.GetGuestInfraInformer().Lister()
		infra, err := infraLister.Get(infrastructureName)
		if err != nil {
			return err
		}
		if infra.Status.PlatformStatus == nil || infra.Status.PlatformStatus.AWS == nil {
			return nil
		}

		userTags := infra.Status.PlatformStatus.AWS.ResourceTags
		if len(userTags) == 0 {
			return nil
		}

		tagPairs := make([]string, 0, len(userTags))
		for _, userTag := range userTags {
			pair := fmt.Sprintf("%s=%s", userTag.Key, userTag.Value)
			tagPairs = append(tagPairs, pair)
		}
		tags := strings.Join(tagPairs, ",")
		tagsArgument := fmt.Sprintf("--extra-tags=%s", tags)

		for i := range deployment.Spec.Template.Spec.Containers {
			container := &deployment.Spec.Template.Spec.Containers[i]
			if container.Name != "csi-driver" {
				continue
			}
			container.Args = append(container.Args, tagsArgument)
		}
		return nil
	}
	informers := []factory.Informer{
		c.GetGuestInfraInformer().Informer(),
	}
	return hook, informers
}

// withAWSRegion sets AWS_REGION env. var from infrastructure.Status.PlatformStatus.AWS.Region
func withAWSRegion(c *clients.Clients) (dc.DeploymentHookFunc, []factory.Informer) {
	hook := func(_ *opv1.OperatorSpec, deployment *appsv1.Deployment) error {
		infraLister := c.GetGuestInfraInformer().Lister()
		infra, err := infraLister.Get(infrastructureName)
		if err != nil {
			return err
		}

		if infra.Status.PlatformStatus == nil || infra.Status.PlatformStatus.AWS == nil {
			return nil
		}

		region := infra.Status.PlatformStatus.AWS.Region
		if region == "" {
			return nil
		}

		for i := range deployment.Spec.Template.Spec.Containers {
			container := &deployment.Spec.Template.Spec.Containers[i]
			if container.Name != "csi-driver" {
				continue
			}
			container.Env = append(container.Env, corev1.EnvVar{
				Name:  "AWS_REGION",
				Value: region,
			})
		}
		return nil
	}
	informers := []factory.Informer{
		c.GetGuestInfraInformer().Informer(),
	}
	return hook, informers
}

// withKMSKeyHook checks for AWSCSIDriverConfigSpec in the ClusterCSIDriver object.
// If it contains KMSKeyARN, it sets the corresponding parameter in the StorageClass.
// This allows the admin to specify a customer managed key to be used by default.
func withKMSKeyHook(c *clients.Clients) csistorageclasscontroller.StorageClassHookFunc {
	hook := func(_ *opv1.OperatorSpec, class *storagev1.StorageClass) error {
		ccdLister := c.GuestOperatorInformers.Operator().V1().ClusterCSIDrivers().Lister()
		ccd, err := ccdLister.Get(class.Provisioner)
		if err != nil {
			return err
		}

		driverConfig := ccd.Spec.DriverConfig
		if driverConfig.DriverType != opv1.AWSDriverType || driverConfig.AWS == nil {
			klog.V(4).Infof("No AWSCSIDriverConfigSpec defined for %s", class.Provisioner)
			return nil
		}

		arn := driverConfig.AWS.KMSKeyARN
		if arn == "" {
			klog.V(4).Infof("Not setting empty %s parameter in StorageClass %s", kmsKeyID, class.Name)
			return nil
		}

		if class.Parameters == nil {
			class.Parameters = map[string]string{}
		}
		klog.V(4).Infof("Setting %s = %s in StorageClass %s", kmsKeyID, arn, class.Name)
		class.Parameters[kmsKeyID] = arn
		return nil
	}
	// Explicitly instantiate ClusterCSIDriver informer, so it is synced during WaitForCacheSync
	// and thus its lister is populated
	_ = c.GuestOperatorInformers.Operator().V1().ClusterCSIDrivers().Informer()
	return hook
}
