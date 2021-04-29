/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
    "reflect"
    "unsafe"
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/intstr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	admissionregistrationv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	webhookv1 "github.com/youngpig1998/webhook-operator/api/v1"
)


const (
	// deployment中的APP标签名
	APP_NAME = "audit-webhook"
	VOLUME_PATCH = "{\"name\":\"internal-tls\",\"secret\":{\"secretName\":\"internal-tls\",\"defaultMode\":420}}" 
	CONTAINER_PATCH = "{\"name\":\"sidecar\",\"image\":\"fanzhan1/fluent:1.10-plugin-script\",\"securityContext\":{\"runAsNonRoot\":true},\"resources\":{\"requests\":{\"memory\":\"100Mi\",\"cpu\":\"100m\"},\"limits\":{\"memory\":\"250Mi\",\"cpu\":\"250m\"}},\"imagePullPolicy\":\"IfNotPresent\",\"args\":[\"/bin/bash\",\"-c\",\"fluentd -c /fluentd/etc/fluent.conf\"],\"volumeMounts\":[{\"name\":\"varlog\",\"mountPath\":\"/var/log\"}]}"
	// 单个POD的CPU资源申请
	CPU_REQUEST = "300m"
	// 单个POD的CPU资源上限
	CPU_LIMIT = "500m"
	// 单个POD的内存资源申请
	MEM_REQUEST = "100Mi"
	// 单个POD的内存资源上限
	MEM_LIMIT = "200Mi"

)



// WebHookReconciler reconciles a WebHook object
type WebHookReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=webhook.example.com,resources=webhooks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=webhook.example.com,resources=webhooks/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=webhook.example.com,resources=webhooks/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=mutatingwebhookconfigurations,verbs=get;list;watch;create;update;patch;delete

func (r *WebHookReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("webhook", req.NamespacedName)

	// your logic here

	log.Info("1. start reconcile logic")

	// 实例化数据结构
	instance := &webhookv1.WebHook{}

	// 通过客户端工具查询，查询条件是
	err := r.Get(ctx, req.NamespacedName, instance)

	if err != nil {

		// 如果没有实例，就返回空结果，这样外部就不再立即调用Reconcile方法了
		if errors.IsNotFound(err) {
			log.Info("2.1. instance not found, maybe removed")
			return reconcile.Result{}, nil
		}

		log.Error(err, "2.2 error")
		// 返回错误信息给外部
		return ctrl.Result{}, err
	}

	// log.Info("3. instance : " + instance.String())

	// 查找deployment
	deployment := &appsv1.Deployment{}

	// 用客户端工具查询
	err = r.Get(ctx, req.NamespacedName, deployment)

	// 查找时发生异常，以及查出来没有结果的处理逻辑
	if err != nil {
		// 如果没有实例就要创建了
		if errors.IsNotFound(err) {
			log.Info("4. deployment not exists")

			// 如果对Size没有需求，此时又没有deployment，就啥事都不做了
			if instance.Spec.Size < 1 {
				log.Info("5.1 not need deployment")
				// 返回
				return ctrl.Result{}, nil
			}

			// 先要创建service
			if err = createServiceIfNotExists(ctx, r, instance, req); err != nil {
				log.Error(err, "5.2 error")
				// 返回错误信息给外部
				return ctrl.Result{}, err
			}

			// 再创建configmap
			if err = createConfigmapIfNotExists(ctx, r, instance, req); err != nil {
				log.Error(err, "5.2 error")
				// 返回错误信息给外部
				return ctrl.Result{}, err
			}

			// 再创建secret
			if err = createSecretIfNotExists(ctx, r, instance, req); err != nil {
				log.Error(err, "5.2 error")
				// 返回错误信息给外部
				return ctrl.Result{}, err
			}

			// 立即创建deployment
			if err = createDeployment(ctx, r, instance); err != nil {
				log.Error(err, "5.3 error")
				// 返回错误信息给外部
				return ctrl.Result{}, err
			}

			// 如果创建成功就更新状态
			if err = updateStatus(ctx, r, instance); err != nil {
				log.Error(err, "5.4. error")
				// 返回错误信息给外部
				return ctrl.Result{}, err
			}


			// deployment创建后立即创建MutatingWebhookConfiguration
			if err = createMutatingWebhookConfigurationIfNotExists(ctx, r, instance, req); err != nil {
				log.Error(err, "5.5 error")
				// 返回错误信息给外部
				return ctrl.Result{}, err
			}






			// 创建成功就可以返回了
			return ctrl.Result{}, nil
		} else {
			log.Error(err, "7. error")
			// 返回错误信息给外部
			return ctrl.Result{}, err
		}
	}


	// Update the WebHook status with the pod names
	// List the pods for this WebHook's deployment
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(instance.Namespace),
	}
	if err = r.List(ctx, podList, listOpts...); err != nil {
		log.Error(err, "Failed to list pods", "WebHook.Namespace", instance.Namespace)
		return ctrl.Result{}, err
	}
	podNames := getPodNames(podList.Items)

	// Update status.Nodes if needed
	if !reflect.DeepEqual(podNames, instance.Status.Nodes) {
		instance.Status.Nodes = podNames
		err := r.Status().Update(ctx, instance)
		if err != nil {
			log.Error(err, "Failed to update WebHook's status")
			return ctrl.Result{}, err
		}
	}

	

	return ctrl.Result{}, nil
	
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebHookReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&webhookv1.WebHook{}).
		Complete(r)
}




// 新建service
func createServiceIfNotExists(ctx context.Context, r *WebHookReconciler, webHook *webhookv1.WebHook, req ctrl.Request) error {
	log := r.Log.WithValues("func", "createService")

	service := &corev1.Service{}

	err := r.Get(ctx, req.NamespacedName, service)

	// 如果查询结果没有错误，证明service正常，就不做任何操作
	if err == nil {
		log.Info("service exists")
		return nil
	}

	// 如果错误不是NotFound，就返回错误
	if !errors.IsNotFound(err) {
		log.Error(err, "query service error")
		return err
	}

	// 实例化一个数据结构
	service = &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: webHook.Namespace,
			Name:      "audit-webhook-service",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{
				Port:     443,
				TargetPort: intstr.IntOrString{
					IntVal: 8081,
					StrVal: "8081",
				},
				Protocol: corev1.ProtocolTCP,
			},
			},
			Selector: map[string]string{
				"app": APP_NAME,
			},
		},
	}

	// 这一步非常关键！
	// 建立关联后，删除webhook资源时就会将service也删除掉
	log.Info("set reference")
	if err := controllerutil.SetControllerReference(webHook, service, r.Scheme); err != nil {
		log.Error(err, "SetControllerReference error")
		return err
	}

	// 创建service
	log.Info("start create service")
	if err := r.Create(ctx, service); err != nil {
		log.Error(err, "create service error")
		return err
	}

	log.Info("create service success")

	return nil
}

// 新建configmap
func createConfigmapIfNotExists(ctx context.Context, r *WebHookReconciler, webHook *webhookv1.WebHook, req ctrl.Request) error {
	log := r.Log.WithValues("func", "createConfigmap")

	configmap := &corev1.ConfigMap{}

	err := r.Get(ctx, req.NamespacedName, configmap)

	// 如果查询结果没有错误，证明configmap正常，就不做任何操作
	if err == nil {
		log.Info("configmap exists")
		return nil
	}

	// 如果错误不是NotFound，就返回错误
	if !errors.IsNotFound(err) {
		log.Error(err, "query configmap error")
		return err
	}

	// 实例化一个数据结构
	configmap = &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: webHook.Namespace,
			Name:      "audit-webhook-configmap",
		},
		Data: map[string]string{
			"volume_patch": VOLUME_PATCH,
			"container_patch": CONTAINER_PATCH,
		},
	}

	// 这一步非常关键！
	// 建立关联后，删除elasticweb资源时就会将service也删除掉
	log.Info("set reference")
	if err := controllerutil.SetControllerReference(webHook, configmap, r.Scheme); err != nil {
		log.Error(err, "SetControllerReference error")
		return err
	}

	// 创建configmap
	log.Info("start create configmap")
	if err := r.Create(ctx, configmap); err != nil {
		log.Error(err, "create configmap error")
		return err
	}

	log.Info("create configmap success")

	return nil
}


// 新建secret
func createSecretIfNotExists(ctx context.Context, r *WebHookReconciler, webHook *webhookv1.WebHook, req ctrl.Request) error {
	log := r.Log.WithValues("func", "createSecret")

	secret := &corev1.Secret{}

	err := r.Get(ctx, req.NamespacedName, secret)

	// 如果查询结果没有错误，证明secret正常，就不做任何操作
	if err == nil {
		log.Info("secret exists")
		return nil
	}

	// 如果错误不是NotFound，就返回错误
	if !errors.IsNotFound(err) {
		log.Error(err, "query secret error")
		return err
	}

	// 实例化一个数据结构
	secret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: webHook.Namespace,
			Name:      "audit-webhook-tls-secret",
			Labels: map[string]string{
				"app": APP_NAME,
			},
		},
		Data: map[string][]byte{
			"tls.crt": stringtoslicebyte("LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURZekNDQWtzQ0ZGU09KVGtEQ3pLaDVwL2phL3dhaCtISEg0RzFNQTBHQ1NxR1NJYjNEUUVCQ3dVQU1JR3oKTVFzd0NRWURWUVFHRXdKVlV6RUxNQWtHQTFVRUNBd0NUbGt4RURBT0JnTlZCQWNNQjBOdmJHOW5ibVV4RHpBTgpCZ05WQkJFTUJqUXlOREkwTWpFVU1CSUdBMVVFQ1F3TFNHRnNiRzhnSURFMk1qRXhEREFLQmdOVkJBb01BMGxDClRURVBNQTBHQTFVRUN3d0dWMkYwYzI5dU1STXdFUVlEVlFRRERBcDNZWFJ6YjI0dWIzSm5NU293S0FZSktvWkkKaHZjTkFRa0JGaHRvWVhKcGJtRnlZWGxoYm1GdUxtMXZhR0Z1UUdsaWJTNWpiMjB3SGhjTk1qQXdPVEk0TVRrMQpOalU0V2hjTk1qQXhNREk0TVRrMU5qVTRXakFvTVNZd0pBWURWUVFEREIxaGRXUnBkQzEzWldKb2IyOXJMWE5sCmNuWnBZMlV1ZW1WdUxuTjJZekNDQVNJd0RRWUpLb1pJaHZjTkFRRUJCUUFEZ2dFUEFEQ0NBUW9DZ2dFQkFQYlMKaHFLSmxXazFjUnpjcFRUcUJJeWtxVFljaVpQMG9uUkloK3J1S0JvbWlPUnNMMUJHSk1wTE5MVjNYemNwK3Y3WQpCZnU5dHpUSHBzWGtEZkhrTnErVkFaNnFRcXAxYlRyNUpvTExaeVRWZ3k1RUUwUnFtQUtTUGVqV3pud3QrZE9zClRwdmgreEwzbUFjVEg1TDFmYnRNMGlHWXYyOE5zOTRkdFBDbG1YZ2pPUlBqSnh2Ni9pL1dKZ2JvTzhaVVgwVVYKNWVjWjJpYWR6VXJBVG03Rm10TWh4OEttOWQvY1p6UHZrVitLQVl2Q0paUWg1M29GckVTd1BicUtKMDBKZGFubwplenJlMTdFK0pDY0UxUFVwVHNERWJzcEhJTjNHYmNac3d6RitXcEYzYjJVeWY2ZHNZbk5ZVTUxR2QyMk1pSVRvClowcG8yNEgwV3N4RDRBUzE4MEVDQXdFQUFUQU5CZ2txaGtpRzl3MEJBUXNGQUFPQ0FRRUExZng4OXB0UVFpSisKdVRxYzIzYmJrYXhYMGZyZktxaTBQaW1QclBodXB1Yk9ObmRRL3BjMGFOYkEvVHkwR3I5enpkUnh5c0tEMHRIVgpFYTFGRkZCa1pvUCt3QVdCT0Ivc2x2OU4xSVVINUk1ajRaOXVlWG4ralpSWU5VdllsUkFnY0FoMEVIYllpS3dFCjFEd05oUURiQW5NK2RVUnlqQ3ZoMEQ5VE9PYWpoV25ieXNPVTV4eEpCbW1pSGw3QWMrVU5GcndsaE8ybkFlSTkKOFNEdWtJZU5WSlpMYkVnb1hQMEdEWURJMTlZa1NXMHFnR2htc2Y4ejN4cUJnbEtVOUVuQXFrRDNlVUQ2N2JQZgpiZmZJQjVhaFkxYzlSeElnVHYraWl3dG8wUHNmNmhzTURFdDA2ZFV5bWJyQmQrN1hJTERVbmFpQTBvai92VFl3CnlYRG5lTi9LNHc9PQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCg=="),
  			"tls.key": stringtoslicebyte("LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQpNSUlFcEFJQkFBS0NBUUVBOXRLR29vbVZhVFZ4SE55bE5Pb0VqS1NwTmh5SmsvU2lkRWlINnU0b0dpYUk1R3d2ClVFWWt5a3MwdFhkZk55bjYvdGdGKzcyM05NZW14ZVFOOGVRMnI1VUJucXBDcW5WdE92a21nc3RuSk5XRExrUVQKUkdxWUFwSTk2TmJPZkMzNTA2eE9tK0g3RXZlWUJ4TWZrdlY5dTB6U0laaS9idzJ6M2gyMDhLV1plQ001RStNbgpHL3IrTDlZbUJ1Zzd4bFJmUlJYbDV4bmFKcDNOU3NCT2JzV2EweUhId3FiMTM5eG5NKytSWDRvQmk4SWxsQ0huCmVnV3NSTEE5dW9vblRRbDFxZWg3T3Q3WHNUNGtKd1RVOVNsT3dNUnV5a2NnM2NadHhtekRNWDVha1hkdlpUSi8KcDJ4aWMxaFRuVVozYll5SWhPaG5TbWpiZ2ZSYXpFUGdCTFh6UVFJREFRQUJBb0lCQUR4dWcwUmNoMWFCSFRiQgoxemxEYXVXOGt5bUtoeXpRb3MzeHpFVjdGaHFCQU5kY25hRDc2NW9VRzgycWNvZWhJYkV2MXhjeDloOVlHcjhzCi9UVVNlVWs0SkhOaW9IdjMwRXkySC9XNk00RFRQaEVmM2MvTWdYZHZzdlRGVXowWVRLakU4V0k5VENueXNTaGEKU0VyRkRJbkZYMVdXZnBpRU5Gdlh6aXQxZ0VQbmNML1p1NmVZUHZ2ejNUQml4Tk5aWFRFQTJqSzdpWHQrWE1rWgovR2xkM1VOV2pMTXBQSFY4NTBZVzF3a0lRU0N5MWp3bjRSZzdKQ1VhMzNKVTlXODN4U01nN2dQRDdhYlJwd29QCldRais2T1BpRGR3eFBLcllURXE0WUxrMFRTbThIeVFGc0JxR3VMYkFWTDVHcnZ6VXYwYjdmZnQ4T2dOa1pGd2wKR2FIZUs2RUNnWUVBLzg0dkVjcWRzc2VSSjFmUjlXcnVUc1N0ejgzRExsRDFrU3VpN2lJRlJvenhrTGNXcytKawpoMXp2UlBQT3d0NUo0MVB4Mkpwb1JMaHpVMHNQOCtMeHdKNkdUNU14Tmt2dUZ4ZExYMnpTeHlqS2FXQk9LdHJqCm1LcE95eVFvUUJJckZGL3owWmd2SzA2eFNSTWhwVEpJRXRRWUY5VkNabHllUm9YOVUwM0JEclVDZ1lFQTl3S1gKdVlkS2ZjbUxWWmthVnZxMnNDSDlSU3VZSk1BZkh0N0dmMnV5cVpQalVNU1Jrbk9CMFU1RnZxKzFGWUF4UjZqSAovRHpBVm9scDdmVkRJWUxWamtMM2MzZ3E4WW1TVTZXbnU0aW9jS09UZ1FYTGdvaXhwTWpUYUlEa1IrYnluWlF1ClBzS1ZFaXQvQVIzenVlU3NNUmVjSC82VFpvR0xid3VDV1d3eTNkMENnWUVBOVVsR0JTOWVTK0hsSSs2bjIwWnYKd0lRRGpyRmxLUEprcHBGTEtFRGpBaVdBTlIzNjNQNkhHdTFZV2F2WFpUQTFkWkEyNVZZYUNWczg2bStkbW1UUgpIN3hpV2Nkd2R2b1VFWHc2d0FQZmtTMWgrZTFveHRzaFJuQjRJWDVJUWplcHExM2VzK25Ud1JreUVqb1FGeEhCCjNwd1ZoalR0K0sxeTczam4wb3RLUmNrQ2dZRUE5c0h5VEhjcEpXdjM4N1VWS1JzZzhlZWltajBvcWwzN09OMlkKTXFhbVB0M3NVajFzcDM4WWlyM0UwdSs0MlJmTkl5Y3JVWUpuS292djlMWDFNRDhCbERLMS9QWnBBQTVNelo5Sgpad0RvTkU1VkJxbUJXbyt2MTB5QVZYK2RqVzdicEN2cDN1eUgrelRVbFlzVWRmcEpRbW14b0F5enQ4MW1PN0tsCnJ5dDF6VWtDZ1lCbTJJKzlZZzA4QzhuK1J1TjN4OXNLbUNIbFBHcmtjdmZwdmhRZ25JMUtTZSsvSlM1RkJqSDQKNlQ0M3dzVWRaUmpVbHB1czY3dmI5aWZvNzh1TU5MdEUyUXkzcTkzNThhR2E1ZlpaaUFjSGlZb01mTnZRL2UzRQpNSnVlYjIzNjdiemdnc3Yybmdyc2JmR0ZJODF2eTlsNjBLcThNT0lVWFB4aEliOVZ1UVhOdHc9PQotLS0tLUVORCBSU0EgUFJJVkFURSBLRVktLS0tLQo="),
		},
	}

	// 这一步非常关键！
	// 建立关联后，删除elasticweb资源时就会将service也删除掉
	log.Info("set reference")
	if err := controllerutil.SetControllerReference(webHook, secret, r.Scheme); err != nil {
		log.Error(err, "SetControllerReference error")
		return err
	}

	// 创建secret
	log.Info("start create secret")
	if err := r.Create(ctx, secret); err != nil {
		log.Error(err, "create secret error")
		return err
	}

	log.Info("create secret success")

	return nil
}




// 新建deployment
func createDeployment(ctx context.Context, r *WebHookReconciler, webHook *webhookv1.WebHook) error {
	log := r.Log.WithValues("func", "createDeployment")

	
	isRunAsRoot := false
	pIsRunAsRoot := &isRunAsRoot //bool类型指针

	log.Info(fmt.Sprintf("expectReplicas 1"))

	// 实例化一个数据结构
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: webHook.Namespace,
			Name:      "audit-webhook-server",
			Labels: map[string]string{
				"app": APP_NAME,
			},
		},
		Spec: appsv1.DeploymentSpec{
			// 副本数是计算出来的
			Replicas: pointer.Int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": APP_NAME,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": APP_NAME,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Image:           "docker.io/fanzhan1/audit-webhook:v0.1.0",
						ImagePullPolicy: "IfNotPresent",
						Name:            APP_NAME,
						Command: []string{"/audit-webhook"},
						Ports: []corev1.ContainerPort{{
							ContainerPort: 8081,
						}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								"cpu":    resource.MustParse(CPU_REQUEST),
								"memory": resource.MustParse(MEM_REQUEST),
							},
							Limits: corev1.ResourceList{
								"cpu":    resource.MustParse(CPU_LIMIT),
								"memory": resource.MustParse(MEM_LIMIT),
							},
						},
						SecurityContext: &corev1.SecurityContext{
							RunAsNonRoot: pIsRunAsRoot,
						},
						Env: []corev1.EnvVar{
							{
								Name: "VOLUME_PATCH",
								ValueFrom: &corev1.EnvVarSource{
									ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "audit-webhook-configmap",
										},
										Key: "volume_patch",
									},
								},
							},
							{
								Name: "CONTAINER_PATCH",
								ValueFrom: &corev1.EnvVarSource{
									ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "audit-webhook-configmap",
										},
										Key: "container_patch",
									},
								},

							},
						},
						VolumeMounts: []corev1.VolumeMount{
							{
								MountPath: "/certs",
								Name: "certs",
								ReadOnly: false,
							},
						},
					}},
					Volumes: []corev1.Volume{
						{
							Name: "certs",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: "audit-webhook-tls-secret",
								},
							},
						},
					},
				},
			},
		},
	}

	// 这一步非常关键！
	// 建立关联后，删除elasticweb资源时就会将deployment也删除掉
	log.Info("set reference")
	if err := controllerutil.SetControllerReference(webHook, deployment, r.Scheme); err != nil {
		log.Error(err, "SetControllerReference error")
		return err
	}

	// 创建deployment
	log.Info("start create deployment")
	if err := r.Create(ctx, deployment); err != nil {
		log.Error(err, "create deployment error")
		return err
	}

	log.Info("create deployment success")

	return nil
}

// 完成了pod的处理后，更新最新状态
func updateStatus(ctx context.Context, r *WebHookReconciler, webHook *webhookv1.WebHook) error {
	// log := r.Log.WithValues("func", "updateStatus")


	return nil
}


// 新建MutatingWebhookConfiguration
func createMutatingWebhookConfigurationIfNotExists(ctx context.Context, r *WebHookReconciler, webHook *webhookv1.WebHook, req ctrl.Request) error {
	log := r.Log.WithValues("func", "MutatingWebhookConfiguration")

	mc := &admissionregistrationv1beta1.MutatingWebhookConfiguration{}

	err := r.Get(ctx, req.NamespacedName, mc)

	path := "/add-sidecar"

	
	failurePolicy := new(admissionregistrationv1beta1.FailurePolicyType)
	*failurePolicy  = admissionregistrationv1beta1.Ignore

	
	matchPolicy  := new(admissionregistrationv1beta1.MatchPolicyType)
	*matchPolicy =  admissionregistrationv1beta1.Equivalent

	scope  := new(admissionregistrationv1beta1.ScopeType)
	*scope = admissionregistrationv1beta1.NamespacedScope


	// 如果查询结果没有错误，证明mc正常，就不做任何操作
	if err == nil {
		log.Info("MutatingWebhookConfiguration exists")
		return nil
	}

	// 如果错误不是NotFound，就返回错误
	if !errors.IsNotFound(err) {
		log.Error(err, "query MutatingWebhookConfiguration error")
		return err
	}

	// 实例化一个数据结构
	mc = &admissionregistrationv1beta1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: webHook.Namespace,
			Name:      "audit-webhook-config",
		},
		Webhooks: []admissionregistrationv1beta1.MutatingWebhook{{
			Name:      "audit.watson.org",
			MatchPolicy: matchPolicy,
			ObjectSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"cp4d-audit": "yes",
				},
			},
			Rules: []admissionregistrationv1beta1.RuleWithOperations{{
				Operations: []admissionregistrationv1beta1.OperationType{admissionregistrationv1beta1.Create},
				Rule: admissionregistrationv1beta1.Rule{
					APIGroups: []string{""},
					APIVersions: []string{"v1"},
					Resources: []string{"pods"},
					Scope: scope,
				},
			},
			},
			ClientConfig: admissionregistrationv1beta1.WebhookClientConfig{
				Service: &admissionregistrationv1beta1.ServiceReference{
					Name: "audit-webhook-service",
					Namespace: "default",
					Path: &path,
					Port: pointer.Int32Ptr(443),
				},
				CABundle: stringtoslicebyte("LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUQ3ekNDQXRjQ0ZCTlM0bXQ2bmt0SWpZZmtnYjA5Y2lFbjVPbEZNQTBHQ1NxR1NJYjNEUUVCRFFVQU1JR3oKTVFzd0NRWURWUVFHRXdKVlV6RUxNQWtHQTFVRUNBd0NUbGt4RURBT0JnTlZCQWNNQjBOdmJHOW5ibVV4RHpBTgpCZ05WQkJFTUJqUXlOREkwTWpFVU1CSUdBMVVFQ1F3TFNHRnNiRzhnSURFMk1qRXhEREFLQmdOVkJBb01BMGxDClRURVBNQTBHQTFVRUN3d0dWMkYwYzI5dU1STXdFUVlEVlFRRERBcDNZWFJ6YjI0dWIzSm5NU293S0FZSktvWkkKaHZjTkFRa0JGaHRvWVhKcGJtRnlZWGxoYm1GdUxtMXZhR0Z1UUdsaWJTNWpiMjB3SGhjTk1qQXdPVEk0TVRrMQpOalU0V2hjTk1qQXhNREk0TVRrMU5qVTRXakNCc3pFTE1Ba0dBMVVFQmhNQ1ZWTXhDekFKQmdOVkJBZ01BazVaCk1SQXdEZ1lEVlFRSERBZERiMnh2WjI1bE1ROHdEUVlEVlFRUkRBWTBNalF5TkRJeEZEQVNCZ05WQkFrTUMwaGgKYkd4dklDQXhOakl4TVF3d0NnWURWUVFLREFOSlFrMHhEekFOQmdOVkJBc01CbGRoZEhOdmJqRVRNQkVHQTFVRQpBd3dLZDJGMGMyOXVMbTl5WnpFcU1DZ0dDU3FHU0liM0RRRUpBUlliYUdGeWFXNWhjbUY1WVc1aGJpNXRiMmhoCmJrQnBZbTB1WTI5dE1JSUJJakFOQmdrcWhraUc5dzBCQVFFRkFBT0NBUThBTUlJQkNnS0NBUUVBM2huSzltUkkKdTByT2pOZDhjOWlZaVI3dXArNWY5NVBGaEhLcTNlR1JTbWhqbWNOaVBhTjJHMEFLWUZkZEtXajh0YkNlbGg3ZwpDaVZpcFU3c2lEazMzRkRnWFFad0xrS2hMTThDWlllSm9TWmd3RUFUemZnWkltNHhxdWwrcmRHSXJIRkdOTW1KCjFlb1hCcEdEVks0NDR5SUhhdUx3elR4c3Q2MWZzdzlCeVR5M2N1UndFdC9DUkcvWE5ibVJ0Wi9HSm40dHJGcFQKMVpsYlRtVysvT08vS0R4UEZVcmJrQzNhNjB0NlZhNHJnckIrR0FxbTRLbmMvUmpYTy9EMEZuejE1bUFrNGtUeApaTjJDUEVpQWRpYytORE5GVW1Ra0IyajBqZjhraHNwQVVUdFdUS3REd1ZveStjS0p4bTl5MklJTmtMM3RJRzNJCis3VW1YakhiQXRZS1RRSURBUUFCTUEwR0NTcUdTSWIzRFFFQkRRVUFBNElCQVFETmJoQ0Nob25YVURpZWRIR0oKbXNzZWJPWE9WYUpCTUhxc2NrVGowaisyMFRPS211c2xZU1hMTTJaSGpPNmNvdTB3Z1VZZ0VYZjBZRTJSdWRVdQpOQkZBMWRFcWVVV2FIZUxFeUwxS1AxWU1SWnlWWG9WOUZVMnBpS0hjK1hJSkVqSnB6ajg3Mm9PTmh4MHVpcUpZCkQ2eUVnWTJqVlZsWXdCWGE1K1JOdU1ROVJXemRkS1I2VzlSZExhdWdxbUJ6b2poYkx2MmJzVUJNSDE0SVZ4blMKSkFZYnh2NkdINmFzWXNPRkQySmRyMVI1MkJhZFNTaGxZd0lNb3NTTmNQQzIwajdZQjMxSmZOYitCU0trSEpSawpWZUYzWUk3YmtoaXBqajZhSncrZFJXckZaRVpjTXRNNm0xYlpvQ1JWWUhIcmUrbUxZVDhSaTF4bUVXaHpsMnZyCjlvTTYKLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo="),
			},
			FailurePolicy: failurePolicy,

		},
		},
	}

	// 这一步非常关键！
	// 建立关联后，删除webhook资源时就会将service也删除掉
	log.Info("set reference")
	if err := controllerutil.SetControllerReference(webHook, mc, r.Scheme); err != nil {
		log.Error(err, "SetControllerReference error")
		return err
	}

	// 创建MutatingWebhookConfiguration
	log.Info("start create MutatingWebhookConfiguration")
	if err := r.Create(ctx, mc); err != nil {
		log.Error(err, "create MutatingWebhookConfiguration error")
		return err
	}

	log.Info("create MutatingWebhookConfiguration success")

	return nil
}







//string转成[]byte
func stringtoslicebyte(s string) []byte {
    sh := (*reflect.StringHeader)(unsafe.Pointer(&s))
    bh := reflect.SliceHeader{
        Data: sh.Data,
        Len:  sh.Len,
        Cap:  sh.Len,
    }
    return *(*[]byte)(unsafe.Pointer(&bh))
}




// getPodNames returns the pod names of the array of pods passed in
func getPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}

