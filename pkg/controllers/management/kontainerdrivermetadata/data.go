package kontainerdrivermetadata

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/rancher/kontainer-driver-metadata/rke"

	mVersion "github.com/mcuadros/go-version"
	"github.com/rancher/rancher/pkg/settings"
	"github.com/rancher/rke/util"

	"github.com/rancher/rancher/pkg/namespace"
	v3 "github.com/rancher/types/apis/management.cattle.io/v3"
	"k8s.io/apimachinery/pkg/api/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	APIVersion                = "management.cattle.io/v3"
	RancherVersionDev         = "2.3"
	sendRKELabel              = "io.cattle.rke_store"
	rkeSystemImageKind        = "RkeK8sSystemImage"
	rkeServiceOptionKind      = "RkeK8sServiceOption"
	rkeAddonKind              = "RkeAddon"
	rkeWindowsSystemImageKind = "RkeK8sWindowsSystemImage"
)

var existLabel = map[string]string{sendRKELabel: "false"}

func (md *MetadataController) createOrUpdateMetadata(data Data) error {
	if err := md.saveSystemImages(data.K8sVersionRKESystemImages,
		data.K8sVersionInfo, data.K8sVersionServiceOptions, data.RancherDefaultK8sVersions); err != nil {
		return err
	}
	if err := md.saveServiceOptions(data.K8sVersionServiceOptions); err != nil {
		return err
	}
	if err := md.saveAddons(data.K8sVersionedTemplates); err != nil {
		return err
	}
	if err := md.saveWindowsInfo(data.K8sVersionWindowsSystemImages, data.K8sVersionWindowsServiceOptions); err != nil {
		return err
	}
	return nil
}

func (md *MetadataController) createOrUpdateMetadataDefaults() error {
	if err := md.saveSystemImages(rke.DriverData.K8sVersionRKESystemImages,
		rke.DriverData.K8sVersionInfo, rke.DriverData.K8sVersionServiceOptions, rke.DriverData.RancherDefaultK8sVersions); err != nil {
		return err
	}
	if err := md.saveServiceOptions(rke.DriverData.K8sVersionServiceOptions); err != nil {
		return err
	}
	if err := md.saveAddons(rke.DriverData.K8sVersionedTemplates); err != nil {
		return err
	}
	if err := md.saveWindowsInfo(rke.DriverData.K8sVersionWindowsSystemImages, rke.DriverData.K8sVersionWindowsServiceOptions); err != nil {
		return err
	}
	return nil
}

func (md *MetadataController) saveSystemImages(K8sVersionRKESystemImages map[string]v3.RKESystemImages,
	K8sVersionInfo map[string]v3.K8sVersionInfo,
	ServiceOptions map[string]v3.KubernetesServicesOptions,
	DefaultK8sVersions map[string]string) error {
	maxVersionForMajorK8sVersion := map[string]string{}
	rancherVersion := GetRancherVersion()
	var deprecated, maxIgnore []string
	for k8sVersion, systemImages := range K8sVersionRKESystemImages {
		rancherVersionInfo, ok := K8sVersionInfo[k8sVersion]
		if ok && toIgnoreForAllK8s(rancherVersionInfo, rancherVersion) {
			deprecated = append(deprecated, k8sVersion)
			continue
		}
		if err := md.createOrUpdateSystemImageCRD(k8sVersion, systemImages); err != nil {
			return err
		}
		majorVersion := util.GetTagMajorVersion(k8sVersion)
		majorVersionInfo, ok := K8sVersionInfo[majorVersion]
		if ok && toIgnoreForK8sCurrent(majorVersionInfo, rancherVersion) {
			maxIgnore = append(maxIgnore, k8sVersion)
			continue
		}
		if curr, ok := maxVersionForMajorK8sVersion[majorVersion]; !ok || k8sVersion > curr {
			maxVersionForMajorK8sVersion[majorVersion] = k8sVersion
		}
	}
	logrus.Debugf("driverMetadata deprecated %v max incompatible versions %v", deprecated, maxIgnore)
	return updateSettings(maxVersionForMajorK8sVersion, rancherVersion, ServiceOptions, DefaultK8sVersions)
}

func toIgnoreForAllK8s(rancherVersionInfo v3.K8sVersionInfo, rancherVersion string) bool {
	if rancherVersionInfo.DeprecateRancherVersion != "" && mVersion.Compare(rancherVersion, rancherVersionInfo.DeprecateRancherVersion, " >= ") {
		return true
	}
	if rancherVersionInfo.MinRancherVersion != "" && mVersion.Compare(rancherVersion, rancherVersionInfo.MinRancherVersion, "<") {
		// only respect min versions, even if max is present - we need to support upgraded clusters
		return true
	}
	return false
}

func toIgnoreForK8sCurrent(majorVersionInfo v3.K8sVersionInfo, rancherVersion string) bool {
	if majorVersionInfo.MaxRancherVersion != "" && mVersion.Compare(rancherVersion, majorVersionInfo.MaxRancherVersion, ">") {
		// include in K8sVersionCurrent only if less then max version
		return true
	}
	return false
}

func (md *MetadataController) saveServiceOptions(K8sVersionServiceOptions map[string]v3.KubernetesServicesOptions) error {
	rkeDataKeys := getRKEVendorOptions()
	for k8sVersion, serviceOptions := range K8sVersionServiceOptions {
		if err := md.createOrUpdateServiceOptionCRD(k8sVersion, serviceOptions, rkeDataKeys); err != nil {
			return err
		}
	}
	return nil
}

func (md *MetadataController) saveAddons(K8sVersionedTemplates map[string]map[string]string) error {
	for addon, templateData := range K8sVersionedTemplates {
		if err := md.createOrUpdateAddonCRD(addon, templateData); err != nil {
			return err
		}
	}
	return nil
}

func (md *MetadataController) saveWindowsInfo(K8sVersionWindowsSystemImages map[string]v3.WindowsSystemImages,
	K8sVersionWindowsServiceOptions map[string]v3.KubernetesServicesOptions) error {
	for k8sVersion, sysImages := range K8sVersionWindowsSystemImages {
		if err := md.createOrUpdateWindowsSystemImageCRD(k8sVersion, sysImages, true); err != nil {
			return err
		}
	}
	for k8sVersion, serviceOptions := range K8sVersionWindowsServiceOptions {
		if err := md.createOrUpdateWindowsServiceOptionCRD(k8sVersion, serviceOptions); err != nil {
			return err
		}
	}
	return nil
}

func (md *MetadataController) createOrUpdateSystemImageCRD(k8sVersion string, systemImages v3.RKESystemImages) error {
	sysImage, err := md.getRKESystemImage(k8sVersion)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		sysImage = &v3.RKEK8sSystemImage{
			ObjectMeta: metav1.ObjectMeta{
				Name:      k8sVersion,
				Namespace: namespace.GlobalNamespace,
			},
			SystemImages: systemImages,
			TypeMeta: metav1.TypeMeta{
				Kind:       rkeSystemImageKind,
				APIVersion: APIVersion,
			},
		}
		if _, err := md.SystemImages.Create(sysImage); err != nil && !errors.IsAlreadyExists(err) {
			return err
		}
		return nil

	}
	if reflect.DeepEqual(sysImage.SystemImages, systemImages) {
		return nil
	}
	sysImageCopy := sysImage.DeepCopy()
	sysImageCopy.SystemImages = systemImages
	if _, err := md.SystemImages.Update(sysImageCopy); err != nil {
		return err
	}
	return nil
}

func (md *MetadataController) createOrUpdateWindowsSystemImageCRD(k8sVersion string, systemImages v3.WindowsSystemImages, windows bool) error {
	sysImage, err := md.getRKEWindowsSystemImage(k8sVersion)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		sysImage = &v3.RKEK8sWindowsSystemImage{
			ObjectMeta: metav1.ObjectMeta{
				Name:      getWindowsName(k8sVersion),
				Namespace: namespace.GlobalNamespace,
			},
			SystemImages: systemImages,
			TypeMeta: metav1.TypeMeta{
				Kind:       rkeWindowsSystemImageKind,
				APIVersion: APIVersion,
			},
		}
		if _, err := md.WindowsSystemImages.Create(sysImage); err != nil && !errors.IsAlreadyExists(err) {
			return err
		}
		return nil

	}
	if reflect.DeepEqual(sysImage.SystemImages, systemImages) {
		return nil
	}
	sysImageCopy := sysImage.DeepCopy()
	sysImageCopy.SystemImages = systemImages
	if _, err := md.WindowsSystemImages.Update(sysImageCopy); err != nil {
		return err
	}
	return nil
}

func (md *MetadataController) createOrUpdateServiceOptionCRD(k8sVersion string, serviceOptions v3.KubernetesServicesOptions, rkeDataKeys map[string]bool) error {
	svcOption, err := md.getRKEServiceOption(k8sVersion)
	_, exists := rkeDataKeys[k8sVersion]
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		svcOption = &v3.RKEK8sServiceOption{
			ObjectMeta: metav1.ObjectMeta{
				Name:      k8sVersion,
				Namespace: namespace.GlobalNamespace,
			},
			ServiceOptions: serviceOptions,
			TypeMeta: metav1.TypeMeta{
				Kind:       rkeServiceOptionKind,
				APIVersion: APIVersion,
			},
		}
		if exists {
			svcOption.Labels = existLabel
		}
		if _, err := md.ServiceOptions.Create(svcOption); err != nil && !errors.IsAlreadyExists(err) {
			return err
		}
		return nil
	}
	var svcOptionCopy *v3.RKEK8sServiceOption
	if reflect.DeepEqual(svcOption.ServiceOptions, serviceOptions) {
		if reflect.DeepEqual(svcOption.Labels, existLabel) && exists {
			return nil
		}
		svcOptionCopy = svcOption.DeepCopy()
		if exists {
			svcOptionCopy.Labels = existLabel
		} else {
			delete(svcOptionCopy.Labels, sendRKELabel)
		}
	} else {
		svcOptionCopy = svcOption.DeepCopy()
		svcOptionCopy.ServiceOptions = serviceOptions
	}
	if svcOptionCopy != nil {
		if _, err := md.ServiceOptions.Update(svcOptionCopy); err != nil {
			return err
		}
	}
	return nil
}

func (md *MetadataController) createOrUpdateWindowsServiceOptionCRD(k8sVersion string, serviceOptions v3.KubernetesServicesOptions) error {
	svcOption, err := md.getRKEWindowsServiceOption(k8sVersion)
	if err != nil {
		if !errors.IsNotFound(err) {
			return err
		}
		svcOption = &v3.RKEK8sServiceOption{
			ObjectMeta: metav1.ObjectMeta{
				Name:      getWindowsName(k8sVersion),
				Namespace: namespace.GlobalNamespace,
			},
			ServiceOptions: serviceOptions,
			TypeMeta: metav1.TypeMeta{
				Kind:       rkeServiceOptionKind,
				APIVersion: APIVersion,
			},
		}
		if _, err := md.ServiceOptions.Create(svcOption); err != nil && !errors.IsAlreadyExists(err) {
			return err
		}
		return nil
	}
	if reflect.DeepEqual(svcOption.ServiceOptions, serviceOptions) {
		return nil
	}
	svcOptionCopy := svcOption.DeepCopy()
	svcOptionCopy.ServiceOptions = serviceOptions
	if svcOptionCopy != nil {
		if _, err := md.ServiceOptions.Update(svcOptionCopy); err != nil {
			return err
		}
	}
	return nil
}

func (md *MetadataController) createOrUpdateAddonCRD(addonName string, templateData map[string]string) error {
	rkeDataKeys := getRKEVendorData(addonName)
	logrus.Debugf("addons %s rkeDataKeys %v", addonName, rkeDataKeys)
	for k8sVersion, template := range templateData {
		_, exists := rkeDataKeys[k8sVersion]
		name := fmt.Sprintf("%s-%s", strings.ToLower(addonName), k8sVersion)
		addon, err := md.getRKEAddon(name)
		if err != nil {
			if !errors.IsNotFound(err) {
				return err
			}
			addon = &v3.RKEAddon{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace.GlobalNamespace,
				},
				Template: template,
				TypeMeta: metav1.TypeMeta{
					Kind:       rkeAddonKind,
					APIVersion: APIVersion,
				},
			}
			if exists {
				addon.Labels = existLabel
			}
			if _, err := md.Addons.Create(addon); err != nil && !errors.IsAlreadyExists(err) {
				return err
			}
			continue
		}
		var addonCopy *v3.RKEAddon
		if reflect.DeepEqual(addon.Template, template) {
			if reflect.DeepEqual(addon.Labels, existLabel) && exists {
				continue
			}
			addonCopy = addon.DeepCopy()
			if exists {
				addonCopy.Labels = existLabel
			} else {
				delete(addonCopy.Labels, sendRKELabel)
			}
		}
		if addonCopy != nil {
			if _, err := md.Addons.Update(addonCopy); err != nil {
				return err
			}
		}
	}
	return nil
}

func getRKEVendorData(addonName string) map[string]bool {
	keys := map[string]bool{}
	templateData, ok := rke.DriverData.K8sVersionedTemplates[addonName]
	if !ok {
		return keys
	}
	for k8sVersion := range templateData {
		keys[k8sVersion] = true
	}
	return keys
}

func getRKEVendorOptions() map[string]bool {
	keys := map[string]bool{}
	for k8sVersion := range rke.DriverData.K8sVersionServiceOptions {
		keys[k8sVersion] = true
	}
	return keys
}

func (md *MetadataController) getRKEAddon(name string) (*v3.RKEAddon, error) {
	return md.AddonsLister.Get(namespace.GlobalNamespace, name)
}

func (md *MetadataController) getRKEServiceOption(k8sVersion string) (*v3.RKEK8sServiceOption, error) {
	return md.ServiceOptionsLister.Get(namespace.GlobalNamespace, k8sVersion)
}

func (md *MetadataController) getRKEWindowsServiceOption(k8sVersion string) (*v3.RKEK8sServiceOption, error) {
	return md.ServiceOptionsLister.Get(namespace.GlobalNamespace, getWindowsName(k8sVersion))
}

func (md *MetadataController) getRKESystemImage(k8sVersion string) (*v3.RKEK8sSystemImage, error) {
	return md.SystemImagesLister.Get(namespace.GlobalNamespace, k8sVersion)
}

func (md *MetadataController) getRKEWindowsSystemImage(k8sVersion string) (*v3.RKEK8sWindowsSystemImage, error) {
	return md.WindowsSystemImagesLister.Get(namespace.GlobalNamespace, getWindowsName(k8sVersion))
}

func getWindowsName(str string) string {
	return fmt.Sprintf("w%s", str)
}

func updateSettings(maxVersionForMajorK8sVersion map[string]string, rancherVersion string,
	K8sVersionServiceOptions map[string]v3.KubernetesServicesOptions, DefaultK8sVersions map[string]string) error {
	k8sVersionRKESystemImages := map[string]interface{}{}
	k8sVersionSvcOptions := map[string]v3.KubernetesServicesOptions{}

	for majorVersion, k8sVersion := range maxVersionForMajorK8sVersion {
		k8sVersionRKESystemImages[k8sVersion] = nil
		k8sVersionSvcOptions[k8sVersion] = K8sVersionServiceOptions[majorVersion]
	}

	var keys []string
	for k := range maxVersionForMajorK8sVersion {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return SaveSettings(k8sVersionRKESystemImages, k8sVersionSvcOptions, DefaultK8sVersions, rancherVersion, keys)
}

func SaveSettings(k8sCurrVersions map[string]interface{},
	k8sVersionSvcOptions map[string]v3.KubernetesServicesOptions, rancherDefaultK8sVersions map[string]string,
	rancherVersion string, maxVersions []string) error {
	k8sCurrVersionData, err := marshal(k8sCurrVersions)
	if err != nil {
		return err
	}
	var versions []string
	for k := range k8sCurrVersions {
		versions = append(versions, k)
	}
	sort.Strings(versions)
	if err := settings.KubernetesVersionToSystemImages.Set(k8sCurrVersionData); err != nil {
		return err
	}
	if err := settings.KubernetesVersionsCurrent.Set(strings.Join(versions, ",")); err != nil {
		return err
	}
	k8sSvcOptionData, err := marshal(k8sVersionSvcOptions)
	if err != nil {
		return err
	}
	if err := settings.KubernetesVersionToServiceOptions.Set(k8sSvcOptionData); err != nil {
		return err
	}
	defaultK8sVersion, ok := rancherDefaultK8sVersions[rancherVersion]
	if !ok || defaultK8sVersion == "" {
		defaultK8sVersion = rancherDefaultK8sVersions["default"]
	}
	if err := settings.KubernetesVersion.Set(defaultK8sVersion); err != nil {
		return err
	}
	if len(maxVersions) > 0 {
		minVersion := maxVersions[0]
		maxVersion := util.GetTagMajorVersion(defaultK8sVersion)
		uiSupported := fmt.Sprintf(">=%s.x <=%s.x", minVersion, maxVersion)
		uiDefaultRange := fmt.Sprintf("<=%s.x", maxVersion)

		if err := settings.UIKubernetesSupportedVersions.Set(uiSupported); err != nil {
			return err
		}
		if err := settings.UIKubernetesDefaultVersion.Set(uiDefaultRange); err != nil {
			return err
		}
	}

	return nil
}

func marshal(data interface{}) (string, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
