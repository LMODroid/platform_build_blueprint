// Copyright 2024 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package blueprint

import (
	"fmt"
	"maps"
	"slices"

	"github.com/google/blueprint/pool"
)

// TransitionMutator implements a top-down mechanism where a module tells its
// direct dependencies what variation they should be built in but the dependency
// has the final say.
//
// When implementing a transition mutator, one needs to implement four methods:
//   - Split() that tells what variations a module has by itself
//   - OutgoingTransition() where a module tells what it wants from its
//     dependency
//   - IncomingTransition() where a module has the final say about its own
//     variation
//   - Mutate() that changes the state of a module depending on its variation
//
// That the effective variation of module B when depended on by module A is the
// composition the outgoing transition of module A and the incoming transition
// of module B.
//
// The outgoing transition should not take the properties of the dependency into
// account, only those of the module that depends on it. For this reason, the
// dependency is not even passed into it as an argument. Likewise, the incoming
// transition should not take the properties of the depending module into
// account and is thus not informed about it. This makes for a nice
// decomposition of the decision logic.
//
// A given transition mutator only affects its own variation; other variations
// stay unchanged along the dependency edges.
//
// Soong makes sure that all modules are created in the desired variations and
// that dependency edges are set up correctly. This ensures that "missing
// variation" errors do not happen and allows for more flexible changes in the
// value of the variation among dependency edges (as opposed to bottom-up
// mutators where if module A in variation X depends on module B and module B
// has that variation X, A must depend on variation X of B)
//
// The limited power of the context objects passed to individual mutators
// methods also makes it more difficult to shoot oneself in the foot. Complete
// safety is not guaranteed because no one prevents individual transition
// mutators from mutating modules in illegal ways and for e.g. Split() or
// Mutate() to run their own visitations of the transitive dependency of the
// module and both of these are bad ideas, but it's better than no guardrails at
// all.
//
// This model is pretty close to Bazel's configuration transitions. The mapping
// between concepts in Soong and Bazel is as follows:
//   - Module == configured target
//   - Variant == configuration
//   - Variation name == configuration flag
//   - Variation == configuration flag value
//   - Outgoing transition == attribute transition
//   - Incoming transition == rule transition
//
// The Split() method does not have a Bazel equivalent and Bazel split
// transitions do not have a Soong equivalent.
//
// Mutate() does not make sense in Bazel due to the different models of the
// two systems: when creating new variations, Soong clones the old module and
// thus some way is needed to change it state whereas Bazel creates each
// configuration of a given configured target anew.
type TransitionMutator interface {
	// Split returns the set of variations that should be created for a module no matter
	// who depends on it. Used when Make depends on a particular variation or when
	// the module knows its variations just based on information given to it in
	// the Blueprint file. This method should not mutate the module it is called
	// on.
	Split(ctx BaseModuleContext) []TransitionInfo

	// OutgoingTransition is called on a module to determine which variation it wants
	// from its direct dependencies. The dependency itself can override this decision.
	// This method should not mutate the module itself.
	OutgoingTransition(ctx OutgoingTransitionContext, sourceTransitionInfo TransitionInfo) TransitionInfo

	// IncomingTransition is called on a module to determine which variation it should
	// be in based on the variation modules that depend on it want. This gives the module
	// a final say about its own variations. This method should not mutate the module
	// itself.
	IncomingTransition(ctx IncomingTransitionContext, incomingTransitionInfo TransitionInfo) TransitionInfo

	// Mutate is called after a module was split into multiple variations on each
	// variation.  It should not split the module any further but adding new dependencies
	// is fine. Unlike all the other methods on TransitionMutator, this method is
	// allowed to mutate the module.
	Mutate(ctx BottomUpMutatorContext, transitionInfo TransitionInfo)

	// TransitionInfoFromVariation is called when adding dependencies with an explicit variation after the
	// TransitionMutator has already run.  It takes a variation name and returns a TransitionInfo for that
	// variation.  It may not be possible for some TransitionMutators to generate an appropriate TransitionInfo
	// if the variation does not contain all the information from the TransitionInfo, in which case the
	// TransitionMutator can panic in TransitionInfoFromVariation, and adding dependencies with explicit variations
	// for this TransitionMutator is not supported.
	TransitionInfoFromVariation(string) TransitionInfo
}

type IncomingTransitionContext interface {
	// Module returns the target of the dependency edge for which the transition
	// is being computed
	Module() Module

	// ModuleName returns the name of the module.  This is generally the value that was returned by Module.Name() when
	// the module was created, but may have been modified by calls to BottomUpMutatorContext.Rename.
	ModuleName() string

	// DepTag() Returns the dependency tag through which this dependency is
	// reached
	DepTag() DependencyTag

	// Config returns the config object that was passed to
	// Context.PrepareBuildActions.
	Config() interface{}

	// Provider returns the value for a provider for the target of the dependency edge for which the
	// transition is being computed.  If the value is not set it returns nil and false.  It panics if
	// called  before the appropriate mutator or GenerateBuildActions pass for the provider.  The value
	// returned may be a deep copy of the value originally passed to SetProvider.
	//
	// This method shouldn't be used directly, prefer the type-safe android.ModuleProvider instead.
	Provider(provider AnyProviderKey) (any, bool)

	// IsAddingDependency returns true if the transition is being called while adding a dependency
	// after the transition mutator has already run, or false if it is being called when the transition
	// mutator is running.  This should be used sparingly, all uses will have to be removed in order
	// to support creating variants on demand.
	IsAddingDependency() bool

	// ModuleErrorf reports an error at the line number of the module type in the module definition.
	ModuleErrorf(fmt string, args ...interface{})

	// PropertyErrorf reports an error at the line number of a property in the module definition.
	PropertyErrorf(property, fmt string, args ...interface{})
}

type OutgoingTransitionContext interface {
	// Module returns the source of the dependency edge for which the transition
	// is being computed
	Module() Module

	// ModuleName returns the name of the module.  This is generally the value that was returned by Module.Name() when
	// the module was created, but may have been modified by calls to BottomUpMutatorContext.Rename.
	ModuleName() string

	// DepTag() Returns the dependency tag through which this dependency is
	// reached
	DepTag() DependencyTag

	// Config returns the config object that was passed to
	// Context.PrepareBuildActions.
	Config() interface{}

	// Provider returns the value for a provider for the source of the dependency edge for which the
	// transition is being computed.  If the value is not set it returns nil and false.  It panics if
	// called before the appropriate mutator or GenerateBuildActions pass for the provider.  The value
	// returned may be a deep copy of the value originally passed to SetProvider.
	//
	// This method shouldn't be used directly, prefer the type-safe android.ModuleProvider instead.
	Provider(provider AnyProviderKey) (any, bool)

	// ModuleErrorf reports an error at the line number of the module type in the module definition.
	ModuleErrorf(fmt string, args ...interface{})

	// PropertyErrorf reports an error at the line number of a property in the module definition.
	PropertyErrorf(property, fmt string, args ...interface{})
}

type TransitionInfo interface {
	// Variation returns a string that will be used as the variation name for modules that use this TransitionInfo
	// as their configuration.  It must return a unique value for each valid TransitionInfo in order to avoid
	// conflicts, and all identical TransitionInfos must return the same value.
	Variation() string
}

type transitionMutatorImpl struct {
	name          string
	mutator       TransitionMutator
	index         int
	inputVariants map[*moduleGroup][]*moduleInfo
	neverFar      bool
}

// Adds each argument in items to l if it's not already there.
func addToStringListIfNotPresent(l []string, items ...string) []string {
	for _, i := range items {
		if !slices.Contains(l, i) {
			l = append(l, i)
		}
	}

	return l
}

func (t *transitionMutatorImpl) addRequiredVariation(m *moduleInfo, variation string, transitionInfo TransitionInfo) {
	m.incomingTransitionInfosLock.Lock()
	defer m.incomingTransitionInfosLock.Unlock()

	// This is only a consistency check. Leaking the variations of a transition
	// mutator to another one could well lead to issues that are difficult to
	// track down.
	if m.currentTransitionMutator != "" && m.currentTransitionMutator != t.name {
		panic(fmt.Errorf("transition mutator is %s in mutator %s", m.currentTransitionMutator, t.name))
	}

	m.currentTransitionMutator = t.name
	if existing, exists := m.incomingTransitionInfos[variation]; exists {
		if existing != transitionInfo {
			panic(fmt.Errorf("TransitionInfo %#v and %#v are different but have same variation %q",
				existing, transitionInfo, variation))
		}
	} else {
		if m.incomingTransitionInfos == nil {
			m.incomingTransitionInfos = make(map[string]TransitionInfo)
		}
		m.incomingTransitionInfos[variation] = transitionInfo
	}
}

func (t *transitionMutatorImpl) propagateMutator(mctx BaseModuleContext) {
	module := mctx.(*mutatorContext).module
	mutatorSplits := t.mutator.Split(mctx)
	if mutatorSplits == nil || len(mutatorSplits) == 0 {
		panic(fmt.Errorf("transition mutator %s returned no splits for module %s", t.name, mctx.ModuleName()))
	}

	// transitionVariations for given a module can be mutated by the module itself
	// and modules that directly depend on it. Since this is a top-down mutator,
	// all modules that directly depend on this module have already been processed
	// so no locking is necessary.
	// Sort the module transitions, but keep the mutatorSplits in the order returned
	// by Split, as the order can be significant when inter-variant dependencies are
	// used.
	transitionVariations := slices.Sorted(maps.Keys(module.incomingTransitionInfos))
	transitionInfoMap := module.incomingTransitionInfos
	module.incomingTransitionInfos = nil

	splitsVariations := make([]string, 0, len(mutatorSplits))
	for _, splitTransitionInfo := range mutatorSplits {
		splitVariation := splitTransitionInfo.Variation()
		splitsVariations = append(splitsVariations, splitVariation)
		if transitionInfoMap == nil {
			transitionInfoMap = make(map[string]TransitionInfo, len(mutatorSplits))
		}
		transitionInfoMap[splitVariation] = splitTransitionInfo
	}

	transitionVariations = addToStringListIfNotPresent(splitsVariations, transitionVariations...)

	outgoingTransitionVariationCache := make([][]string, len(transitionVariations))
	transitionInfos := make([]TransitionInfo, 0, len(transitionVariations))
	for srcVariationIndex, srcVariation := range transitionVariations {
		srcVariationTransitionCache := make([]string, len(module.directDeps))
		for depIndex, dep := range module.directDeps {
			transitionInfo := t.transition(mctx)(mctx.moduleInfo(), transitionInfoMap[srcVariation], dep.module, dep.tag)
			variation := transitionInfo.Variation()
			srcVariationTransitionCache[depIndex] = variation
			t.addRequiredVariation(dep.module, variation, transitionInfo)
		}
		outgoingTransitionVariationCache[srcVariationIndex] = srcVariationTransitionCache
		transitionInfos = append(transitionInfos, transitionInfoMap[srcVariation])
	}
	module.outgoingTransitionCache = outgoingTransitionVariationCache
	module.splitTransitionVariations = transitionVariations
	module.splitTransitionInfos = transitionInfos
}

var (
	outgoingTransitionContextPool = pool.New[outgoingTransitionContextImpl]()
	incomingTransitionContextPool = pool.New[incomingTransitionContextImpl]()
)

type transitionContextImpl struct {
	context     *Context
	source      *moduleInfo
	dep         *moduleInfo
	depTag      DependencyTag
	postMutator bool
	config      interface{}
	errs        []error
}

func (c *transitionContextImpl) DepTag() DependencyTag {
	return c.depTag
}

func (c *transitionContextImpl) Config() interface{} {
	return c.config
}

func (c *transitionContextImpl) IsAddingDependency() bool {
	return c.postMutator
}

func (c *transitionContextImpl) error(err error) {
	if err != nil {
		c.errs = append(c.errs, err)
	}
}

func (c *transitionContextImpl) ModuleErrorf(fmt string, args ...interface{}) {
	c.error(c.context.moduleErrorf(c.dep, fmt, args...))
}

func (c *transitionContextImpl) PropertyErrorf(property, fmt string, args ...interface{}) {
	c.error(c.context.PropertyErrorf(c.dep.logicModule, property, fmt, args...))
}

type outgoingTransitionContextImpl struct {
	transitionContextImpl
}

func (c *outgoingTransitionContextImpl) Module() Module {
	return c.source.logicModule
}

func (c *outgoingTransitionContextImpl) ModuleName() string {
	return c.source.group.name
}

func (c *outgoingTransitionContextImpl) Provider(provider AnyProviderKey) (any, bool) {
	return c.context.provider(c.source, provider.provider())
}

type incomingTransitionContextImpl struct {
	transitionContextImpl
}

func (c *incomingTransitionContextImpl) Module() Module {
	return c.dep.logicModule
}

func (c *incomingTransitionContextImpl) ModuleName() string {
	return c.dep.group.name
}

func (c *incomingTransitionContextImpl) Provider(provider AnyProviderKey) (any, bool) {
	return c.context.provider(c.dep, provider.provider())
}

func (t *transitionMutatorImpl) transition(mctx BaseModuleContext) Transition {
	return func(source *moduleInfo, sourceTransitionInfo TransitionInfo, dep *moduleInfo, depTag DependencyTag) TransitionInfo {
		tc := transitionContextImpl{
			context: mctx.base().context,
			source:  source,
			dep:     dep,
			depTag:  depTag,
			config:  mctx.Config(),
		}
		outCtx := outgoingTransitionContextPool.Get()
		*outCtx = outgoingTransitionContextImpl{tc}
		outgoingTransitionInfo := t.mutator.OutgoingTransition(outCtx, sourceTransitionInfo)
		for _, err := range outCtx.errs {
			mctx.error(err)
		}
		outgoingTransitionContextPool.Put(outCtx)
		outCtx = nil
		if mctx.Failed() {
			return outgoingTransitionInfo
		}
		inCtx := incomingTransitionContextPool.Get()
		*inCtx = incomingTransitionContextImpl{tc}
		finalTransitionInfo := t.mutator.IncomingTransition(inCtx, outgoingTransitionInfo)
		for _, err := range inCtx.errs {
			mctx.error(err)
		}
		incomingTransitionContextPool.Put(inCtx)
		inCtx = nil
		return finalTransitionInfo
	}
}

func (t *transitionMutatorImpl) bottomUpMutator(mctx BottomUpMutatorContext) {
	mc := mctx.(*mutatorContext)
	// Fetch and clean up transition mutator state. No locking needed since the
	// only time interaction between multiple modules is required is during the
	// computation of the variations required by a given module.
	variations := mc.module.splitTransitionVariations
	transitionInfos := mc.module.splitTransitionInfos
	outgoingTransitionCache := mc.module.outgoingTransitionCache
	mc.module.splitTransitionInfos = nil
	mc.module.splitTransitionVariations = nil
	mc.module.outgoingTransitionCache = nil
	mc.module.currentTransitionMutator = ""

	if len(variations) < 1 {
		panic(fmt.Errorf("no variations found for module %s by mutator %s",
			mctx.ModuleName(), t.name))
	}

	if len(variations) == 1 && variations[0] == "" {
		// Module is not split, just apply the transition
		mc.context.convertDepsToVariation(mc.module, 0,
			chooseDepByIndexes(mc.mutator.name, outgoingTransitionCache))
		mc.context.setModuleTransitionInfo(mc.module, t, transitionInfos[0])
	} else {
		modules := mc.createVariationsWithTransition(variations, outgoingTransitionCache)
		for i, module := range modules {
			mc.context.setModuleTransitionInfo(module, t, transitionInfos[i])
		}
	}
}

func (t *transitionMutatorImpl) mutateMutator(mctx BottomUpMutatorContext) {
	module := mctx.(*mutatorContext).module
	t.mutator.Mutate(mctx, module.transitionInfos[t.index])
}

type TransitionMutatorHandle interface {
	// NeverFar causes the variations created by this mutator to never be ignored when adding
	// far variation dependencies. Normally, far variation dependencies ignore all the variants
	// of the source module, and only use the variants explicitly requested by the
	// AddFarVariationDependencies call.
	NeverFar() TransitionMutatorHandle
}

type transitionMutatorHandle struct {
	inner MutatorHandle
	impl  *transitionMutatorImpl
}

var _ TransitionMutatorHandle = (*transitionMutatorHandle)(nil)

func (h *transitionMutatorHandle) NeverFar() TransitionMutatorHandle {
	h.impl.neverFar = true
	return h
}

func (c *Context) RegisterTransitionMutator(name string, mutator TransitionMutator) TransitionMutatorHandle {
	impl := &transitionMutatorImpl{name: name, mutator: mutator}

	c.registerTransitionPropagateMutator(name+"_propagate", impl.propagateMutator)
	bottomUpHandle := c.RegisterBottomUpMutator(name, impl.bottomUpMutator).setTransitionMutator(impl)
	c.RegisterBottomUpMutator(name+"_mutate", impl.mutateMutator)

	impl.index = len(c.transitionMutators)
	c.transitionMutators = append(c.transitionMutators, impl)
	c.transitionMutatorNames = append(c.transitionMutatorNames, name)

	return &transitionMutatorHandle{inner: bottomUpHandle, impl: impl}
}

// This function is called for every dependency edge to determine which
// variation of the dependency is needed. Its inputs are the depending module,
// its variation, the dependency and the dependency tag.
type Transition func(source *moduleInfo, sourceTransitionInfo TransitionInfo, dep *moduleInfo, depTag DependencyTag) TransitionInfo
