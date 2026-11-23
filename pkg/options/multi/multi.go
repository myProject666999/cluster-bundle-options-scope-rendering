// Copyright 2019 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package multi is an applier that performs multiple different applier
// approaches.
package multi

import (
	"fmt"

	bundle "github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/apis/bundle/v1alpha1"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/options"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/options/gotmpl"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/options/patchtmpl"
	log "k8s.io/klog"
)

// ApplierConfig is a config option that can be passed to NewApplierWithConfig
// or NewDefaultApplierWithConfig.
type ApplierConfig func(*applier)

type applier struct {
	appliers []options.Applier

	// unknownComponentPolicy determines how scoped options that reference
	// components other than the one being rendered are handled.
	unknownComponentPolicy options.UnknownComponentPolicy
}

// WithUnknownComponentPolicy sets the policy for handling scoped options
// (see options.ScopedOptionsKey) that reference components not being
// rendered. The default is options.UnknownComponentWarn: a warning is logged
// for every unmatched scoped component key, because a typo in a component
// name would otherwise silently disable the scoped options.
//
// Note that this applier renders one component per ApplyOptions call. When
// components are rendered one-by-one in a loop, scoped keys belonging to
// other components in the loop will also be reported; resolve scoping
// up-front with options.OptionsForComponent and options.UnmatchedScopedKeys
// if that is a concern.
func WithUnknownComponentPolicy(policy options.UnknownComponentPolicy) ApplierConfig {
	return func(a *applier) {
		a.unknownComponentPolicy = policy
	}
}

// NewApplier creates a new multi applier instance.
func NewApplier(appliers []options.Applier) options.Applier {
	return NewApplierWithConfig(appliers)
}

// NewApplierWithConfig creates a new multi applier instance with the
// specified config options.
func NewApplierWithConfig(appliers []options.Applier, cfgs ...ApplierConfig) options.Applier {
	a := &applier{
		appliers:               appliers[:],
		unknownComponentPolicy: options.UnknownComponentWarn,
	}
	for _, cfg := range cfgs {
		cfg(a)
	}
	return a
}

// NewDefaultApplier creates a default multi-applier
func NewDefaultApplier() options.Applier {
	return NewDefaultApplierWithConfig()
}

// NewDefaultApplierWithConfig creates a default multi-applier with the
// specified config options.
func NewDefaultApplierWithConfig(cfgs ...ApplierConfig) options.Applier {
	return NewApplierWithConfig([]options.Applier{
		gotmpl.NewApplier(),
		patchtmpl.NewDefaultApplier(),
	}, cfgs...)
}

// ApplyOptions by going through the appliers in order.
func (m *applier) ApplyOptions(comp *bundle.Component, opts options.JSONOptions) (*bundle.Component, error) {
	comp = comp.DeepCopy()
	if err := m.checkScopedComponents(comp.ComponentReference(), opts); err != nil {
		return nil, err
	}
	for _, a := range m.appliers {
		c, err := a.ApplyOptions(comp, opts)
		if err != nil {
			return nil, err
		}
		comp = c
	}
	return comp, nil
}

// checkScopedComponents applies the unknown-component policy to scoped
// option keys that do not match the component being rendered.
func (m *applier) checkScopedComponents(ref bundle.ComponentReference, opts options.JSONOptions) error {
	unmatched, err := options.UnmatchedScopedKeys(opts, ref)
	if err != nil {
		return err
	}
	for _, key := range unmatched {
		msg := fmt.Sprintf("options contain a scoped entry for component %q, which does not match component %q being rendered; the scoped options will not be applied", key, ref)
		switch m.unknownComponentPolicy {
		case options.UnknownComponentError:
			return fmt.Errorf("%s", msg)
		case options.UnknownComponentIgnore:
			// no-op
		default: // options.UnknownComponentWarn
			log.Warningf("%s", msg)
		}
	}
	return nil
}
