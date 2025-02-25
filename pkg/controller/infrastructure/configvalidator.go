// Copyright (c) 2021 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package infrastructure

import (
	"context"
	"fmt"

	"github.com/gardener/gardener-extension-provider-aws/pkg/apis/aws/helper"
	"github.com/gardener/gardener-extension-provider-aws/pkg/aws"
	awsclient "github.com/gardener/gardener-extension-provider-aws/pkg/aws/client"

	"github.com/gardener/gardener/extensions/pkg/controller/common"
	"github.com/gardener/gardener/extensions/pkg/controller/infrastructure"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// configValidator implements ConfigValidator for aws infrastructure resources.
type configValidator struct {
	common.ClientContext
	awsClientFactory awsclient.Factory
	logger           logr.Logger
}

// NewConfigValidator creates a new ConfigValidator.
func NewConfigValidator(awsClientFactory awsclient.Factory, logger logr.Logger) infrastructure.ConfigValidator {
	return &configValidator{
		awsClientFactory: awsClientFactory,
		logger:           logger.WithName("aws-infrastructure-config-validator"),
	}
}

// Validate validates the provider config of the given infrastructure resource with the cloud provider.
func (c *configValidator) Validate(ctx context.Context, infra *extensionsv1alpha1.Infrastructure) field.ErrorList {
	allErrs := field.ErrorList{}

	logger := c.logger.WithValues("infrastructure", client.ObjectKeyFromObject(infra))

	// Get provider config from the infrastructure resource
	config, err := helper.InfrastructureConfigFromInfrastructure(infra)
	if err != nil {
		allErrs = append(allErrs, field.InternalError(nil, err))
		return allErrs
	}

	// Create AWS client
	credentials, err := aws.GetCredentialsFromSecretRef(ctx, c.Client(), infra.Spec.SecretRef, false)
	if err != nil {
		allErrs = append(allErrs, field.InternalError(nil, fmt.Errorf("could not get AWS credentials: %+v", err)))
		return allErrs
	}
	awsClient, err := c.awsClientFactory.NewClient(string(credentials.AccessKeyID), string(credentials.SecretAccessKey), infra.Spec.Region)
	if err != nil {
		allErrs = append(allErrs, field.InternalError(nil, fmt.Errorf("could not create AWS client: %+v", err)))
		return allErrs
	}

	// Validate infrastructure config
	if config.Networks.VPC.ID != nil {
		logger.Info("Validating infrastructure networks.vpc.id")
		allErrs = append(allErrs, c.validateVPC(ctx, awsClient, *config.Networks.VPC.ID, field.NewPath("networks", "vpc", "id"))...)
	}

	return allErrs
}

func (c *configValidator) validateVPC(ctx context.Context, awsClient awsclient.Interface, vpcID string, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	// Verify that the VPC exists and the enableDnsSupport and enableDnsHostnames VPC attributes are both true
	for _, attribute := range []string{"enableDnsSupport", "enableDnsHostnames"} {
		value, err := awsClient.GetVPCAttribute(ctx, vpcID, attribute)
		if err != nil {
			if awsclient.IsNotFoundError(err) {
				allErrs = append(allErrs, field.NotFound(fldPath, vpcID))
			} else {
				allErrs = append(allErrs, field.InternalError(fldPath, fmt.Errorf("could not get VPC attribute %s for VPC %s: %w", attribute, vpcID, err)))
			}
			return allErrs
		}
		if !value {
			allErrs = append(allErrs, field.Invalid(fldPath, vpcID, fmt.Sprintf("VPC attribute %s must be set to true", attribute)))
		}
	}

	// Verify that there is an internet gateway attached to the VPC
	internetGatewayID, err := awsClient.GetVPCInternetGateway(ctx, vpcID)
	if err != nil {
		allErrs = append(allErrs, field.InternalError(fldPath, fmt.Errorf("could not get internet gateway for VPC %s: %w", vpcID, err)))
		return allErrs
	}
	if internetGatewayID == "" {
		allErrs = append(allErrs, field.Invalid(fldPath, vpcID, "no attached internet gateway found"))
	}

	return allErrs
}
