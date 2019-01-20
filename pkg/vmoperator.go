/* **********************************************************
 * Copyright 2018 VMware, Inc.  All rights reserved. -- VMware Confidential
 * **********************************************************/

package pkg

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// Base FQDN for the vmoperator
	VmOperatorKey string = "vmoperator.vmware.com"

	// VM Operator version key
	VmOperatorVersionKey string = "vmoperator.vmware.com/version"

	// Annotation Key for VM provider
	VmOperatorVmProviderKey string = "vmoperator.vmware.com/vmprovider"

	// Annotation Key for vSphere VC Id.
	VmOperatorVcUuidKey string = "vmoperator.vmware.com/vcuuid"

	// Annotation Key for vSphere Mo Ref
	VmOperatorMorefKey string = "vmoperator.vmware.com/moref"
)

func AddAnnotations(objectMeta *metav1.ObjectMeta) {
	// Add vSphere provider annotations to the object meta
	annotations := objectMeta.GetAnnotations()

	// TODO: Make this version dynamic
	annotations[VmOperatorVersionKey] = "v1"

	objectMeta.SetAnnotations(annotations)
}
