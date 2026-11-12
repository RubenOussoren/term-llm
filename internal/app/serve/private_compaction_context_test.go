package serve

import (
	"context"
	"testing"

	"github.com/samsaffron/term-llm/internal/llm/core"
	"github.com/samsaffron/term-llm/internal/llm/mockprovider"
	"github.com/samsaffron/term-llm/internal/modelpolicy"
)

type privateCompactionContextProvider struct {
	core.Provider
	native bool
	check  func(context.Context)
}

func (p *privateCompactionContextProvider) Stream(ctx context.Context, req core.Request) (core.Stream, error) {
	p.check(ctx)
	return p.Provider.Stream(ctx, req)
}
func (p *privateCompactionContextProvider) NativeCompact(ctx context.Context, req core.NativeCompactRequest) (*core.NativeCompactResult, error) {
	if !p.native {
		return nil, core.ErrNativeCompactUnavailable
	}
	p.check(ctx)
	return &core.NativeCompactResult{PreTokens: 900, PostTokens: 300}, nil
}

func TestLockedCompactionPropagatesAdmissionToProviderContext(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "soft", true: "native"}[native], func(t *testing.T) {
			calls := 0
			provider := &privateCompactionContextProvider{Provider: mockprovider.NewMockProvider("mock").AddTextResponse("compacted fixture"), native: native}
			rt, _ := newServeCompactionTestRuntime(t, provider)
			policy := modelpolicy.Policy{LockSession: true}.With("private", []string{"mock:mock-model"})
			rt.modelPolicy, rt.sessionMeta.ModelPolicy = policy, policy
			if err := rt.store.Update(context.Background(), rt.sessionMeta); err != nil {
				t.Fatal(err)
			}
			type executionKey struct{}
			provider.check = func(ctx context.Context) {
				calls++
				if !core.PrivateSessionFromContext(ctx) || core.SessionIDFromContext(ctx) != rt.sessionMeta.ID {
					t.Fatal("provider did not receive scoped private admission")
				}
				if ctx.Value(executionKey{}) != "execution" {
					t.Fatal("execution context lost")
				}
			}
			prepared, err := rt.prepareCompaction(context.Background(), rt.sessionMeta.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer prepared.release()
			// Asynchronous HTTP scheduling supplies a different execution context.
			if _, err := prepared.execute(context.WithValue(context.Background(), executionKey{}, "execution")); err != nil {
				t.Fatal(err)
			}
			if calls == 0 {
				t.Fatal("provider boundary not exercised")
			}
		})
	}
}
