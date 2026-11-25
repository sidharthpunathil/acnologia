package providers

// Built in OAuth providers for workplace apps: Slack, HubSpot and LinkedIn.
// Endpoints, token answers and error shapes are from each provider's
// official docs, read 2026-11-25 (docs.slack.dev, developers.hubspot.com,
// learn.microsoft.com/linkedin). Default scopes are kept minimal: add the
// ones your calls need on the service, then reconnect.

// slackRefused matches Slack's refusals of the token itself. Slack answers
// errors with HTTP 200 and {"ok": false, "error": "..."}.
var slackRefused = &Match{Status: []int{200}, BodyRegex: `"error"\s*:\s*"(invalid_auth|not_authed|token_revoked|account_inactive)"`}

func oauthAppBuiltins() []Builtin {
	return []Builtin{
		{
			Slug: "slack", Name: "Slack", Category: "Productivity", Color: "#4a154b",
			Description: "The Slack Web API as an app installed in a workspace, with its bot token. Connect one account per workspace.",
			Website:     "https://api.slack.com/apps", DocsURL: "https://docs.slack.dev/authentication/installing-with-oauth",
			Spec: Spec{
				BaseURL: "https://slack.com/api",
				Auth: Auth{Type: AuthOAuth2, Name: "Authorization", OAuth: &OAuthConfig{
					AuthorizeURL:   "https://slack.com/oauth/v2/authorize",
					TokenURL:       "https://slack.com/api/oauth.v2.access",
					Scopes:         []string{"channels:read", "users:read", "team:read"},
					ScopeSeparator: ",",
					ClientAuth:     "basic",
					// The bot token is at the top of the answer; a user
					// token would be authed_user.access_token.
					TokenPath:    "access_token",
					UserinfoURL:  "https://slack.com/api/auth.test",
					AccountField: "team",
				}},
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 3},
				Quota:    QuotaInfo{Unit: "requests", FreeTier: "Free; each Web API method has its own rate limit tier"},
				Rules: []Rule{
					{Name: "Token refused", Match: *slackRefused, Outcome: Invalid},
				},
				UseStandardRules: true,
				Health:           &Health{Method: "GET", Path: "/auth.test", Invalid: slackRefused},
				Examples: []Example{
					{Name: "Who am I", Method: "GET", Path: "/auth.test", Safe: true},
				},
				Guide: &Guide{
					Summary: "Slack's Web API as an installed app: read channels and users, post messages with the right scopes. Each connected workspace is one key.",
					Endpoints: []GuideEndpoint{
						{Name: "Who am I", Method: "GET", Path: "/auth.test", Description: "The workspace and bot the token belongs to. Needs no scope.", Response: "ok, team, team_id, user, user_id, bot_id."},
						{Name: "List channels", Method: "GET", Path: "/conversations.list", Description: "Public channels (channels:read).",
							Params:   []GuideParam{{Name: "limit", In: "query", Description: "Page size, up to 1000"}, {Name: "cursor", In: "query", Description: "response_metadata.next_cursor of the last page"}},
							Response: "channels[] with id, name, is_member."},
						{Name: "List users", Method: "GET", Path: "/users.list", Description: "Members of the workspace (users:read).",
							Params:   []GuideParam{{Name: "limit", In: "query", Description: "Page size"}, {Name: "cursor", In: "query", Description: "Next page cursor"}},
							Response: "members[] with id, name, real_name."},
						{Name: "Post a message", Method: "POST", Path: "/chat.postMessage", Description: "Needs the chat:write scope, and the bot in the channel.",
							Body: map[string]any{"channel": "C0123456789", "text": "Hello from the bot"}, Response: "ok, channel, ts."},
					},
					Notes: []string{
						"Slack answers most errors with HTTP 200: check ok, and read error (missing_scope, channel_not_found, invalid_auth). A refused token marks the key invalid.",
						"Rate limits answer 429 with Retry-After; the pool waits that long before using the workspace's token again.",
						"The default scopes read channels, users and the team. Add the scopes your calls need (chat:write to post) on the service and reconnect.",
						"The bot token is used. For a user token, make a custom provider from this one with authorize_params {\"user_scope\": \"...\"} and token_path authed_user.access_token.",
						"With token rotation on in the Slack app, tokens last 12 hours and are refreshed for you; without it they last until revoked.",
					},
				},
				Tests: []Fixture{
					{Name: "success", Status: 200, Body: `{"ok":true,"team":"Acme"}`, Expect: OK},
					{Name: "invalid auth", Status: 200, Body: `{"ok":false,"error":"invalid_auth"}`, Expect: Invalid},
					{Name: "token revoked", Status: 200, Body: `{"ok":false,"error":"token_revoked"}`, Expect: Invalid},
					{Name: "missing scope", Status: 200, Body: `{"ok":false,"error":"missing_scope","needed":"chat:write"}`, Expect: OK},
					{Name: "rate limited", Status: 429, Headers: map[string]string{"Retry-After": "30"}, Body: `{"ok":false,"error":"ratelimited"}`, Expect: RateLimited},
					{Name: "server error", Status: 503, Body: `oops`, Expect: Error},
				},
			},
		},
		{
			Slug: "hubspot", Name: "HubSpot", Category: "Marketing", Color: "#ff7a59",
			Description: "HubSpot's CRM, marketing and account APIs as a connected HubSpot account. Connect one account per HubSpot portal.",
			Website:     "https://developers.hubspot.com", DocsURL: "https://developers.hubspot.com/docs/api-reference/latest/authentication/manage-oauth-tokens",
			Spec: Spec{
				BaseURL: "https://api.hubapi.com",
				Auth: Auth{Type: AuthOAuth2, Name: "Authorization", OAuth: &OAuthConfig{
					AuthorizeURL: "https://app.hubspot.com/oauth/authorize",
					// v1 of the token endpoint is deprecated (errors from
					// February 2027); v3 takes the same form.
					TokenURL:     "https://api.hubapi.com/oauth/v3/token",
					Scopes:       []string{"oauth"},
					UserinfoURL:  "https://api.hubapi.com/account-info/v3/details",
					AccountField: "portalId",
				}},
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 3},
				Quota:    QuotaInfo{Unit: "requests", FreeTier: "Limits per app and portal depend on the HubSpot subscription"},
				Rules: []Rule{
					{Name: "Missing scope (the caller's)", Match: Match{Status: []int{403}, BodyContains: []string{"missing_scopes", "missing_scope"}}, Outcome: OK},
				},
				UseStandardRules: true,
				Health: &Health{
					Method: "GET", Path: "/account-info/v3/details",
					// A token without that scope still works for the CRM.
					OKAnyway: &Match{Status: []int{403}},
				},
				Examples: []Example{
					{Name: "Account details", Method: "GET", Path: "/account-info/v3/details", Safe: true},
				},
				Guide: &Guide{
					Summary: "HubSpot's APIs as a connected HubSpot account: contacts, companies, deals and the rest of the CRM. Each connected portal is one key.",
					Endpoints: []GuideEndpoint{
						{Name: "Account details", Method: "GET", Path: "/account-info/v3/details", Description: "The portal the token belongs to.", Response: "portalId, timeZone, companyCurrency."},
						{Group: "CRM", Name: "List contacts", Method: "GET", Path: "/crm/v3/objects/contacts", Description: "Needs crm.objects.contacts.read.",
							Params:   []GuideParam{{Name: "limit", In: "query", Description: "Page size, up to 100"}, {Name: "after", In: "query", Description: "paging.next.after of the last page"}, {Name: "properties", In: "query", Description: "Comma separated property names"}},
							Response: "results[] with id and properties, paging.next.after."},
						{Group: "CRM", Name: "Search contacts", Method: "POST", Path: "/crm/v3/objects/contacts/search", Description: "Filter contacts. Needs crm.objects.contacts.read.",
							Body:     map[string]any{"filterGroups": []map[string]any{{"filters": []map[string]any{{"propertyName": "email", "operator": "EQ", "value": "ana@example.com"}}}}, "limit": 10},
							Response: "total and results[]."},
					},
					Notes: []string{
						"The default scope is oauth only. Add the scopes your HubSpot app asks for (crm.objects.contacts.read and so on) on the service: HubSpot refuses a sign in whose scopes differ from the app's required scopes.",
						"A 403 with MISSING_SCOPES is the caller's: the account works, the token lacks that scope.",
						"Access tokens last 30 minutes and are refreshed for you.",
						"Rate limits answer 429; the response headers say which limit was hit.",
					},
				},
				Tests: []Fixture{
					{Name: "success", Status: 200, Body: `{"portalId":123}`, Expect: OK},
					{Name: "missing scopes", Status: 403, Body: `{"status":"error","category":"MISSING_SCOPES","message":"This app hasn't been granted all required scopes"}`, Expect: OK},
					{Name: "expired token", Status: 401, Body: `{"status":"error","category":"EXPIRED_AUTHENTICATION"}`, Expect: Invalid},
					{Name: "rate limited", Status: 429, Body: `{"status":"error","policyName":"TEN_SECONDLY_ROLLING"}`, Expect: RateLimited},
					{Name: "server error", Status: 502, Body: `oops`, Expect: Error},
				},
			},
		},
		{
			Slug: "linkedin", Name: "LinkedIn", Category: "Marketing", Color: "#0a66c2",
			Description: "LinkedIn's APIs as a signed in LinkedIn member: profile through Sign In with LinkedIn, and the products your LinkedIn app is approved for.",
			Website:     "https://www.linkedin.com/developers/apps", DocsURL: "https://learn.microsoft.com/linkedin/shared/authentication/authorization-code-flow",
			Spec: Spec{
				BaseURL: "https://api.linkedin.com",
				Auth: Auth{Type: AuthOAuth2, Name: "Authorization", OAuth: &OAuthConfig{
					AuthorizeURL: "https://www.linkedin.com/oauth/v2/authorization",
					TokenURL:     "https://www.linkedin.com/oauth/v2/accessToken",
					Scopes:       []string{"openid", "profile", "email"},
					AccountField: "email",
				}},
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 3},
				Quota:    QuotaInfo{Unit: "requests", FreeTier: "Daily limits per app and member, shown in the LinkedIn developer portal"},
				Rules: []Rule{
					// A 403 is a product or scope the app lacks, not a bad token.
					{Name: "No access to this API", Match: Match{Status: []int{403}}, Outcome: OK},
				},
				UseStandardRules: true,
				Health:           &Health{Method: "GET", Path: "/v2/userinfo"},
				Examples: []Example{
					{Name: "Me", Method: "GET", Path: "/v2/userinfo", Safe: true},
				},
				Guide: &Guide{
					Summary: "LinkedIn as a signed in member: the OpenID Connect profile, and the APIs of the LinkedIn products your app is approved for.",
					Endpoints: []GuideEndpoint{
						{Name: "Me", Method: "GET", Path: "/v2/userinfo", Description: "The member's OpenID Connect profile.", Response: "sub, name, given_name, family_name, picture, email."},
					},
					Notes: []string{
						"Versioned calls live under /rest and need the headers LinkedIn-Version (a month, YYYYMM) and X-Restli-Protocol-Version: 2.0.0. Send them with the request.",
						"LinkedIn issues refresh tokens only to some approved partners. Without one, an access token lasts 60 days, after which the account must reconnect.",
						"A 403 means the app is not approved for that product or scope; it does not mark the key.",
						"Add the scopes of the products your app is approved for on the service, then reconnect each account.",
					},
				},
				Tests: []Fixture{
					{Name: "success", Status: 200, Body: `{"sub":"abc","email":"ana@example.com"}`, Expect: OK},
					{Name: "not approved", Status: 403, Body: `{"status":403,"serviceErrorCode":100,"message":"Not enough permissions to access"}`, Expect: OK},
					{Name: "expired token", Status: 401, Body: `{"status":401,"serviceErrorCode":65601,"message":"The token used in the request has been revoked by the user"}`, Expect: Invalid},
					{Name: "throttled", Status: 429, Body: `{"status":429,"message":"Resource level throttle limit exceeded"}`, Expect: RateLimited},
					{Name: "server error", Status: 500, Body: `oops`, Expect: Error},
				},
			},
		},
	}
}
