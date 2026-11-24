package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/transformer/typesafe"
)

// Both TypeSafe and Bailian serve the System One protocol, so they must resolve to
// the same outbound transformer and differ only by base URL. Registering the type
// in the ent enum without wiring the biz branch would leave channels of that type
// with a nil Outbound, which only surfaces at request time.
func TestSystemOneChannels_UseTypesafeTransformer(t *testing.T) {
	cases := []struct {
		name       string
		channelTyp channel.Type
		baseURL    string
	}{
		{
			name:       "typesafe",
			channelTyp: channel.TypeTypesafe,
			baseURL:    "https://api.typesafe.ai/v1",
		},
		{
			name:       "bailian_systemone",
			channelTyp: channel.TypeBailianSystemone,
			baseURL:    "https://llm-example.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
			defer client.Close()

			ctx := authz.WithTestBypass(context.Background())

			entChannel := client.Channel.Create().
				SetName("System One " + tc.name).
				SetType(tc.channelTyp).
				SetBaseURL(tc.baseURL).
				SetCredentials(objects.ChannelCredentials{APIKey: "test-key"}).
				SetSupportedModels([]string{"decision-model-preview"}).
				SetDefaultTestModel("decision-model-preview").
				SaveX(ctx)

			channelSvc := NewChannelServiceForTest(client)

			built, err := channelSvc.buildChannelWithTransformer(entChannel)
			require.NoError(t, err)
			require.NotNil(t, built)
			require.NotNil(t, built.Outbound, "channel type %s must wire an outbound transformer", tc.channelTyp)

			_, ok := built.Outbound.(*typesafe.OutboundTransformer)
			require.True(t, ok, "channel type %s should use the typesafe transformer", tc.channelTyp)
		})
	}
}

// The ent enum validator is generated from the schema; a type missing from it is
// rejected at insert time with "invalid enum value". This guards the generated
// artifact against drifting away from the schema.
func TestBailianSystemoneType_IsAcceptedByValidator(t *testing.T) {
	require.NoError(t, channel.TypeValidator(channel.TypeBailianSystemone))
	require.Equal(t, "bailian_systemone", channel.TypeBailianSystemone.String())
}
