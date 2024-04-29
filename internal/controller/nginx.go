package controller

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	simulationv1alpha1 "github.com/930C/simulated-workload-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/json"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	deploymentNameSuffix = "-nginx"
	serviceNameSuffix    = "-nginx-service"
	configMapNameSuffix  = "-nginx-config"
	secretNameSuffix     = "-nginx-secret"
	htmlConfigMapSuffix  = "-nginx-html"
)

func (r *WorkloadReconciler) ReconcileNginxDeployment(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	logger := log.FromContext(ctx)

	// Check if Nginx is enabled in the workload spec
	if workload.Spec.Nginx == nil {
		logger.Info("Nginx deployment not enabled, ensuring any existing resources are deleted")
		return r.cleanupNginxResources(ctx, workload)
	}

	// Ensure ConfigMap is in desired state
	if err := r.reconcileConfigMap(ctx, workload); err != nil {
		logger.Error(err, "Failed to reconcile Nginx ConfigMap")
		return err
	}

	// Ensure Secret is in desired state
	if err := r.reconcileSecret(ctx, workload); err != nil {
		logger.Error(err, "Failed to reconcile Nginx Secret")
		return err
	}

	// Ensure HTML ConfigMap is in desired state
	if err := r.reconcileHTMLConfigMap(ctx, workload); err != nil {
		logger.Error(err, "Failed to reconcile Nginx HTML ConfigMap")
		return err
	}

	// Deploy Nginx
	if err := r.reconcileNginx(ctx, workload); err != nil {
		logger.Error(err, "Failed to deploy Nginx")
		return err
	}

	// Optionally, manage a Service for Nginx
	if err := r.reconcileService(ctx, workload); err != nil {
		logger.Error(err, "Failed to reconcile Nginx Service")
		return err
	}

	// Update status to reflect successful reconciliation
	return r.updateNginxStatus(ctx, workload)
}

func (r *WorkloadReconciler) cleanupNginxResources(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	logger := log.FromContext(ctx)

	// Delete the Deployment if it exists
	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: workload.Name + deploymentNameSuffix, Namespace: workload.Namespace}, deployment)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get Nginx Deployment for deletion")
		return err
	}
	if err == nil {
		err = r.Delete(ctx, deployment)
		if err != nil {
			logger.Error(err, "Failed to delete Nginx Deployment")
			return err
		}
		logger.Info("Nginx Deployment deleted")
	}

	// Delete the Service if it exists
	service := &corev1.Service{}
	err = r.Get(ctx, types.NamespacedName{Name: workload.Name + serviceNameSuffix, Namespace: workload.Namespace}, service)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get Nginx Service for deletion")
		return err
	}
	if err == nil {
		err = r.Delete(ctx, service)
		if err != nil {
			logger.Error(err, "Failed to delete Nginx Service")
			return err
		}
		logger.Info("Nginx Service deleted")
	}

	// Delete the ConfigMap if it exists
	configMap := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: workload.Name + configMapNameSuffix, Namespace: workload.Namespace}, configMap)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get Nginx ConfigMap for deletion")
		return err
	}
	if err == nil {
		err = r.Delete(ctx, configMap)
		if err != nil {
			logger.Error(err, "Failed to delete Nginx ConfigMap")
			return err
		}
		logger.Info("Nginx ConfigMap deleted")
	}

	// Delete the Secret if it exists
	secret := &corev1.Secret{}
	err = r.Get(ctx, types.NamespacedName{Name: workload.Name + secretNameSuffix, Namespace: workload.Namespace}, secret)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get Nginx Secret for deletion")
		return err
	}
	if err == nil {
		err = r.Delete(ctx, secret)
		if err != nil {
			logger.Error(err, "Failed to delete Nginx Secret")
			return err
		}
		logger.Info("Nginx Secret deleted")
	}

	// Delete the HTML ConfigMap if it exists
	htmlConfigMap := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: workload.Name + htmlConfigMapSuffix, Namespace: workload.Namespace}, htmlConfigMap)
	if err != nil && !apierrors.IsNotFound(err) {
		logger.Error(err, "Failed to get Nginx HTML ConfigMap for deletion")
		return err
	}
	if err == nil {
		err = r.Delete(ctx, htmlConfigMap)
		if err != nil {
			logger.Error(err, "Failed to delete Nginx HTML ConfigMap")
			return err
		}
		logger.Info("Nginx HTML ConfigMap deleted")
	}

	return nil
}

func (r *WorkloadReconciler) reconcileConfigMap(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	logger := log.FromContext(ctx)
	configMapName := workload.Name + configMapNameSuffix

	// Calculate the hash of the ConfigMap data
	hash, err := hashMapData(workload.Spec.Nginx.ConfigMapData)
	if err != nil {
		logger.Error(err, "Failed to calculate hash of ConfigMap data")
		return err
	}

	// Defining the ConfigMap object
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: workload.Namespace,
			Annotations: map[string]string{
				"data-hash": hash,
			},
		},
		Data: workload.Spec.Nginx.ConfigMapData,
	}

	// Check if ConfigMap already exists
	foundCM := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: workload.Namespace}, foundCM)
	if err != nil && apierrors.IsNotFound(err) {
		logger.Info("Creating a new ConfigMap", "ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)
		err = r.Create(ctx, cm)
		if err != nil {
			logger.Error(err, "Failed to create new ConfigMap")
			return err
		}

		return r.updateResourceHashStatus(ctx, workload, hash, "configmap")
	} else if err != nil {
		logger.Error(err, "Failed to get ConfigMap")
		return err
	}

	// Don't update the found ConfigMap if data does not match (don't overwrite user changes)

	if foundCM.Annotations["data-hash"] != hash {
		logger.Info("Updating ConfigMap", "ConfigMap.Namespace", foundCM.Namespace, "ConfigMap.Name", foundCM.Name)
		foundCM.Data = cm.Data
		foundCM.Annotations["data-hash"] = hash
		err = r.Update(ctx, foundCM)
		if err != nil {
			logger.Error(err, "Failed to update ConfigMap")
			return err
		}
		return r.updateResourceHashStatus(ctx, workload, hash, "configmap")
	}

	return nil
}

func (r *WorkloadReconciler) reconcileSecret(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	logger := log.FromContext(ctx)
	secretName := workload.Name + secretNameSuffix

	hash, err := hashMapData(workload.Spec.Nginx.SecretData)
	if err != nil {
		logger.Error(err, "Failed to hash secret data")
		return err
	}

	// Defining the Secret object
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: workload.Namespace,
			Annotations: map[string]string{
				"data-hash": hash,
			},
		},
		Data: encodeSecretData(workload.Spec.Nginx.SecretData),
		Type: corev1.SecretTypeOpaque,
	}

	// Check if Secret already exists
	foundSecret := &corev1.Secret{}
	err = r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: workload.Namespace}, foundSecret)
	if err != nil && apierrors.IsNotFound(err) {
		logger.Info("Creating a new Secret", "Secret.Namespace", secret.Namespace, "Secret.Name", secret.Name)
		err = r.Create(ctx, secret)
		if err != nil {
			logger.Error(err, "Failed to create new Secret")
			return err
		}
		return r.updateResourceHashStatus(ctx, workload, hash, "secret")
	} else if err != nil {
		logger.Error(err, "Failed to get Secret")
		return err
	}

	if foundSecret.Annotations["data-hash"] != hash {
		logger.Info("Updating Secret", "Secret.Namespace", foundSecret.Namespace, "Secret.Name", foundSecret.Name)
		foundSecret.Data = encodeSecretData(workload.Spec.Nginx.SecretData)
		foundSecret.Annotations["data-hash"] = hash
		err = r.Update(ctx, foundSecret)
		if err != nil {
			logger.Error(err, "Failed to update Secret")
			return err
		}
		return r.updateResourceHashStatus(ctx, workload, hash, "secret")
	}

	return nil
}

func (r *WorkloadReconciler) reconcileNginx(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	logger := log.FromContext(ctx)
	deploymentName := workload.Name + deploymentNameSuffix

	logger.Info("Deploying Nginx", "Deployment.Namespace", workload.Namespace, "Deployment.Name", deploymentName)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: workload.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: workload.Spec.Nginx.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "nginx", "workload": workload.Name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "nginx", "workload": workload.Name},
					Annotations: map[string]string{
						"configmap-hash": workload.Status.ResourceHashes.ConfigMapHash,
						"secret-hash":    workload.Status.ResourceHashes.SecretHash,
						"html-hash":      workload.Status.ResourceHashes.HtmlHash,
					},
				},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{
						{
							Name:  workload.Name + "-init",
							Image: "busybox",
							Command: []string{"/bin/sh", "-c", `
								apk add --no-cache gettext;
								envsubst < /usr/share/nginx/html/template/index.html > /usr/share/nginx/html/index.html;
							`},
							Env: []corev1.EnvVar{
								{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"}}},
								{Name: "NAMESPACE", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"}}},
								{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"}}},
								{Name: "CONFIG_MESSAGE", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: workload.Name + configMapNameSuffix}, Key: "message"}}},
								{Name: "SECRET_USERNAME", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: workload.Name + secretNameSuffix}, Key: "username"}}},
								{Name: "SECRET_PASSWORD", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: workload.Name + secretNameSuffix}, Key: "password"}}},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "nginx-html",
									ReadOnly:  true,
									MountPath: "/usr/share/nginx/html/template",
								},
								{
									Name:      "nginx-html",
									ReadOnly:  false,
									MountPath: "/usr/share/nginx/html",
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
							Ports: []corev1.ContainerPort{{ContainerPort: 80}},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "nginx-html", MountPath: "/usr/share/nginx/html", ReadOnly: true},
								{Name: "config-volume", MountPath: "/etc/nginx/conf.d"},
								{Name: "secret-volume", MountPath: "/etc/nginx/secrets", ReadOnly: true},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "config-volume", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: workload.Name + configMapNameSuffix}}}},
						{Name: "secret-volume", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: workload.Name + secretNameSuffix}}},
						{Name: "nginx-html", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: workload.Name + htmlConfigMapSuffix}}}},
					},
				},
			},
		},
	}

	foundDeployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: deploymentName, Namespace: workload.Namespace}, foundDeployment)
	if err != nil && apierrors.IsNotFound(err) {
		logger.Info("Creating a new Nginx Deployment", "Deployment.Namespace", deployment.Namespace, "Deployment.Name", deployment.Name)
		err = r.Create(ctx, deployment)
		if err != nil {
			logger.Error(err, "Failed to create new Nginx Deployment")
			return err
		}
		return nil
	} else if err != nil {
		logger.Error(err, "Failed to get Nginx Deployment")
		return err
	}

	// Kopieren für Änderungen
	updatedDeployment := foundDeployment.DeepCopy()

	// Überprüfen und Setzen neuer Werte für Annotations und Replikas
	updateNeeded := false
	if updatedDeployment.Spec.Replicas != workload.Spec.Nginx.Replicas {
		updatedDeployment.Spec.Replicas = workload.Spec.Nginx.Replicas
		updateNeeded = true
	}

	annotations := updatedDeployment.Spec.Template.Annotations
	if annotations["configmap-hash"] != workload.Status.ResourceHashes.ConfigMapHash ||
		annotations["secret-hash"] != workload.Status.ResourceHashes.SecretHash ||
		annotations["html-hash"] != workload.Status.ResourceHashes.HtmlHash {
		annotations["configmap-hash"] = workload.Status.ResourceHashes.ConfigMapHash
		annotations["secret-hash"] = workload.Status.ResourceHashes.SecretHash
		annotations["html-hash"] = workload.Status.ResourceHashes.HtmlHash
		updateNeeded = true
	}

	// Nur aktualisieren, wenn Änderungen erforderlich sind
	if updateNeeded {
		err = r.Update(ctx, updatedDeployment)
		if err != nil {
			logger.Error(err, "Failed to update Nginx Deployment")
			return err
		}
	}

	return nil
}

func (r *WorkloadReconciler) reconcileService(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	logger := log.FromContext(ctx)

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      workload.Name + "-nginx-service",
			Namespace: workload.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app":      "nginx",
				"workload": workload.Name,
			},
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt32(80),
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}

	foundService := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, foundService)
	if err != nil && apierrors.IsNotFound(err) {
		logger.Info("Creating a new Nginx Service", "Service.Namespace", service.Namespace, "Service.Name", service.Name)
		return r.Create(ctx, service)
	} else if err != nil {
		return err
	}

	return nil
}

func (r *WorkloadReconciler) updateNginxStatus(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	logger := log.FromContext(ctx)

	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: workload.Name + "-nginx", Namespace: workload.Namespace}, deployment)
	if err != nil {
		logger.Error(err, "Failed to get Nginx deployment for status update")
		return err
	}

	workload.Status.DeploymentStatus.Replicas = deployment.Status.Replicas
	workload.Status.DeploymentStatus.UpToDateReplicas = deployment.Status.UpdatedReplicas
	workload.Status.DeploymentStatus.AvailableReplicas = deployment.Status.AvailableReplicas
	workload.Status.DeploymentStatus.ReadyReplicas = deployment.Status.ReadyReplicas

	if err := r.Status().Update(ctx, workload); err != nil {
		logger.Error(err, "Failed to update workload status")
		return err
	}

	return nil
}

func (r *WorkloadReconciler) reconcileHTMLConfigMap(ctx context.Context, workload *simulationv1alpha1.Workload) error {
	logger := log.FromContext(ctx)
	configMapName := workload.Name + htmlConfigMapSuffix

	// Calculate the hash of the ConfigMap data
	hash, err := hashMapData(workload.Spec.Nginx.ConfigMapData)
	if err != nil {
		logger.Error(err, "Failed to calculate hash of ConfigMap data")
		return err
	}

	// Defining the ConfigMap object
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: workload.Namespace,
			Annotations: map[string]string{
				"data-hash": hash,
			},
		},
		Data: map[string]string{"index.html": workload.Spec.Nginx.HTML},
	}

	// Check if ConfigMap already exists
	foundCM := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: workload.Namespace}, foundCM)
	if err != nil && apierrors.IsNotFound(err) {
		logger.Info("Creating a new HTML ConfigMap", "ConfigMap.Namespace", cm.Namespace, "ConfigMap.Name", cm.Name)
		err = r.Create(ctx, cm)
		if err != nil {
			logger.Error(err, "Failed to create new HTML ConfigMap")
			return err
		}
		return r.updateResourceHashStatus(ctx, workload, hash, "html")
	} else if err != nil {
		logger.Error(err, "Failed to get HTML ConfigMap")
		return err
	}

	if foundCM.Annotations["data-hash"] != hash {
		logger.Info("Updating HTML ConfigMap", "ConfigMap.Namespace", foundCM.Namespace, "ConfigMap.Name", foundCM.Name)
		foundCM.Data = cm.Data
		foundCM.Annotations["data-hash"] = hash
		err = r.Update(ctx, foundCM)
		if err != nil {
			logger.Error(err, "Failed to update ConfigMap")
			return err
		}
		return r.updateResourceHashStatus(ctx, workload, hash, "html")
	}

	return nil
}

// Helper function to encode secret data
func encodeSecretData(data map[string]string) map[string][]byte {
	encodedData := make(map[string][]byte)
	for k, v := range data {
		encodedData[k] = []byte(base64.StdEncoding.EncodeToString([]byte(v)))
	}
	return encodedData
}

func hashMapData(data map[string]string) (string, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(jsonData)
	return hex.EncodeToString(hash[:]), nil

}

// updateResourceHashStatus generalizes the updating of resource hashes in workload status.
func (r *WorkloadReconciler) updateResourceHashStatus(ctx context.Context, workload *simulationv1alpha1.Workload, hash, resourceType string) error {
	logger := log.FromContext(ctx)

	// Set the new hash in the status based on the resource type
	switch resourceType {
	case "configmap":
		workload.Status.ResourceHashes.ConfigMapHash = hash
	case "secret":
		workload.Status.ResourceHashes.SecretHash = hash
	case "html":
		workload.Status.ResourceHashes.HtmlHash = hash
	default:
		logger.Error(fmt.Errorf("unsupported resource type"), "Cannot update hash status for unsupported resource type", "ResourceType", resourceType)
		return nil
	}

	if err := r.Status().Update(ctx, workload); err != nil {
		logger.Error(err, "Failed to update workload status", "ResourceType", resourceType)
		return err
	}
	return nil
}
