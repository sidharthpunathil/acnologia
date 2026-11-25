package providers

// Built in OAuth providers: APIs that are called with a signed in account's
// access token instead of an API key (oauth.go). Each server registers its
// own OAuth app with the provider and enters the client id and secret on the
// service, with the redirect URI the service page shows.

const (
	googleAuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL     = "https://oauth2.googleapis.com/token"
)

// GoogleAdsAPIVersion is the Google Ads API version the built in examples
// and guide use. Google ships a major version about every quarter and
// sunsets each about a year later (developers.google.com/google-ads/api/docs/sunset-dates,
// read 2026-11-25: v26 is the newest, v23 the oldest still served). To move
// to a newer one, change it here: the paths below follow, and an install's
// stored spec keeps its own paths until the provider is edited.
const GoogleAdsAPIVersion = "v26"

// googleOAuth is Google's sign in, asking for the scopes given plus openid
// and email (which name the account). Google hands out a refresh token only
// with access_type=offline, and again on a later sign in only with
// prompt=consent.
func googleOAuth(scopes ...string) *OAuthConfig {
	return &OAuthConfig{
		AuthorizeURL: googleAuthorizeURL,
		TokenURL:     googleTokenURL,
		Scopes:       append([]string{"openid", "email"}, scopes...),
		AuthorizeParams: map[string]string{
			"access_type": "offline", "prompt": "consent", "include_granted_scopes": "true",
		},
		PKCE:         true,
		AccountField: "email",
	}
}

// googleRules sort Google's 403s: a rate or quota limit moves to another
// account, a permission error is the caller's (the account cannot see that
// property), and anything else falls through to the standard rules.
var googleRules = []Rule{
	{Name: "Rate limited", Match: Match{Status: []int{403}, BodyContains: []string{"ratelimitexceeded", "userratelimitexceeded", "rate_limit_exceeded"}}, Outcome: RateLimited},
	{Name: "Daily quota", Match: Match{Status: []int{403}, BodyContains: []string{"dailylimitexceeded", "quotaexceeded"}}, Outcome: QuotaExhausted},
	{Name: "No access to this resource", Match: Match{Status: []int{403}, BodyContains: []string{"permission_denied", "insufficientpermissions", "forbidden", "access_token_scope_insufficient"}}, Outcome: OK},
}

var googleTests = []Fixture{
	{Name: "success", Status: 200, Body: `{"rows":[]}`, Expect: OK},
	{Name: "rate limited", Status: 403, Body: `{"error":{"code":403,"errors":[{"reason":"rateLimitExceeded"}]}}`, Expect: RateLimited},
	{Name: "daily quota", Status: 403, Body: `{"error":{"code":403,"errors":[{"reason":"quotaExceeded","domain":"youtube.quota"}]}}`, Expect: QuotaExhausted},
	{Name: "no access to the property", Status: 403, Body: `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"User does not have sufficient permission for site"}}`, Expect: OK},
	{Name: "expired token", Status: 401, Body: `{"error":{"code":401,"status":"UNAUTHENTICATED"}}`, Expect: Invalid},
	{Name: "too many requests", Status: 429, Body: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`, Expect: RateLimited},
	{Name: "server error", Status: 503, Body: `oops`, Expect: Error},
}

func oauthBuiltins() []Builtin {
	return []Builtin{
		{
			Slug: "google", Name: "Google APIs", Category: "Analytics", Color: "#4285f4",
			Description: "Search Console, Google Analytics 4, YouTube Data and other Google APIs, called as a signed in Google account. Connect one or more accounts.",
			Website:     "https://console.cloud.google.com", DocsURL: "https://developers.google.com/identity/protocols/oauth2/web-server",
			Spec: Spec{
				BaseURL: "https://www.googleapis.com",
				Auth: Auth{Type: AuthOAuth2, Name: "Authorization", OAuth: googleOAuth(
					"https://www.googleapis.com/auth/webmasters.readonly",
					"https://www.googleapis.com/auth/analytics.readonly",
					"https://www.googleapis.com/auth/youtube.readonly",
				)},
				Defaults:         Defaults{Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 3},
				Quota:            QuotaInfo{Unit: "requests", FreeTier: "Free within each API's own quota"},
				Rules:            googleRules,
				UseStandardRules: true,
				Hosts: []Host{
					{Name: "searchconsole", URL: "https://searchconsole.googleapis.com", Title: "Search Console API", DocsURL: "https://developers.google.com/webmaster-tools/v1/api_reference_index"},
					{Name: "analyticsdata", URL: "https://analyticsdata.googleapis.com", Title: "Google Analytics Data API (GA4)", DocsURL: "https://developers.google.com/analytics/devguides/reporting/data/v1"},
					{Name: "analyticsadmin", URL: "https://analyticsadmin.googleapis.com", Title: "Google Analytics Admin API", DocsURL: "https://developers.google.com/analytics/devguides/config/admin/v1"},
					{Name: "youtube", URL: "https://youtube.googleapis.com", Title: "YouTube Data API", DocsURL: "https://developers.google.com/youtube/v3/docs"},
					{Name: "youtubeanalytics", URL: "https://youtubeanalytics.googleapis.com", Title: "YouTube Analytics API", DocsURL: "https://developers.google.com/youtube/analytics/reference"},
					{Name: "sheets", URL: "https://sheets.googleapis.com", Title: "Google Sheets API", DocsURL: "https://developers.google.com/sheets/api/reference/rest"},
				},
				Health: &Health{
					Method: "GET", Path: "/oauth2/v3/userinfo",
					// A token without the profile scopes still works for the APIs.
					OKAnyway: &Match{Status: []int{403}},
				},
				Examples: []Example{
					{Name: "Search Console sites", Method: "GET", Path: "/~searchconsole/webmasters/v3/sites", Safe: true},
					{Name: "GA4 accounts", Method: "GET", Path: "/~analyticsadmin/v1beta/accountSummaries", Safe: true},
				},
				Guide: &Guide{
					Summary: "Google's APIs as a signed in account: Search Console, GA4 reporting and admin, YouTube Data and Analytics, Sheets. Each connected Google account is one key; requests rotate across accounts that can see the same data.",
					Endpoints: []GuideEndpoint{
						{Group: "Search Console", Name: "List sites", Method: "GET", Path: "/~searchconsole/webmasters/v3/sites", Description: "Properties the account can read.", Response: "siteEntry[] with siteUrl and permissionLevel."},
						{Group: "Search Console", Name: "Search analytics", Method: "POST", Path: "/~searchconsole/webmasters/v3/sites/{siteUrl}/searchAnalytics/query", Description: "Clicks, impressions, CTR and position by query, page, country or date. URL encode siteUrl (sc-domain:example.com becomes sc-domain%3Aexample.com).",
							Body: map[string]any{"startDate": "2026-10-01", "endDate": "2026-10-31", "dimensions": []string{"query"}, "rowLimit": 100}, Response: "rows[] with keys, clicks, impressions, ctr, position."},
						{Group: "Google Analytics", Name: "Account summaries", Method: "GET", Path: "/~analyticsadmin/v1beta/accountSummaries", Description: "GA4 accounts and properties the account can read.", Response: "accountSummaries[] with propertySummaries[].property (properties/123)."},
						{Group: "Google Analytics", Name: "Run a report", Method: "POST", Path: "/~analyticsdata/v1beta/properties/{propertyId}:runReport", Description: "A GA4 report.",
							Body: map[string]any{"dateRanges": []map[string]string{{"startDate": "28daysAgo", "endDate": "today"}}, "dimensions": []map[string]string{{"name": "pagePath"}}, "metrics": []map[string]string{{"name": "screenPageViews"}}}, Response: "rows[] with dimensionValues and metricValues."},
						{Group: "YouTube", Name: "My channels", Method: "GET", Path: "/~youtube/youtube/v3/channels", Description: "The account's channels.", Params: []GuideParam{{Name: "part", In: "query", Required: true, Description: "snippet,statistics"}, {Name: "mine", In: "query", Description: "true"}}},
						{Group: "YouTube", Name: "Search videos", Method: "GET", Path: "/~youtube/youtube/v3/search", Description: "Costs 100 quota units per call.", Params: []GuideParam{{Name: "part", In: "query", Required: true, Description: "snippet"}, {Name: "q", In: "query", Description: "Search text"}}},
					},
					Notes: []string{
						"Pick the API with the /~name path segment: /~searchconsole, /~analyticsdata, /~analyticsadmin, /~youtube, /~youtubeanalytics, /~sheets. Paths without it go to www.googleapis.com.",
						"A 403 PERMISSION_DENIED means the connected account cannot see that site or property. Connect an account that can, or ask for access in Google.",
						"The scopes asked for are set on the service. Add the scope of every API you call, then reconnect each account.",
						"YouTube Data quota is 10,000 units a day per Google Cloud project, shared by every connected account.",
					},
				},
				Tests: googleTests,
			},
		},
		{
			Slug: "google-ads", Name: "Google Ads", Category: "Marketing", Color: "#fbbc04",
			Description: "The Google Ads API as a signed in Google account, with your developer token. Connect one account per manager or advertiser.",
			Website:     "https://ads.google.com", DocsURL: "https://developers.google.com/google-ads/api/docs/start",
			Spec: Spec{
				BaseURL: "https://googleads.googleapis.com",
				Auth: Auth{Type: AuthOAuth2, Name: "Authorization",
					Fields: []AuthField{
						{Key: "developer_token", Label: "Developer token", In: "header", Name: "developer-token"},
						{Key: "login_customer_id", Label: "Manager account ID (login-customer-id)", In: "header", Name: "login-customer-id", Public: true, Optional: true},
					},
					OAuth: googleOAuth("https://www.googleapis.com/auth/adwords"),
				},
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 3},
				Quota:    QuotaInfo{Unit: "operations", FreeTier: "Free; daily operation limits depend on the developer token's access level"},
				Rules: []Rule{
					{Name: "Developer token not usable", Match: Match{Status: []int{401, 403}, BodyContains: []string{"developer_token_not_approved", "developer_token_prohibited", "developer_token_invalid"}}, Outcome: Invalid},
					{Name: "No access to this customer", Match: Match{Status: []int{403}, BodyContains: []string{"user_permission_denied", "customer_not_enabled", "permission_denied"}}, Outcome: OK},
				},
				UseStandardRules: true,
				Examples: []Example{
					{Name: "Accessible customers", Method: "GET", Path: "/" + GoogleAdsAPIVersion + "/customers:listAccessibleCustomers", Safe: true},
				},
				Guide: &Guide{
					Summary: "Google Ads reporting and management as a signed in Google account. The developer token and the optional manager account ID are stored with each connected account and sent for you.",
					Endpoints: []GuideEndpoint{
						{Name: "Accessible customers", Method: "GET", Path: "/" + GoogleAdsAPIVersion + "/customers:listAccessibleCustomers", Description: "Customer accounts the signed in user can reach.", Response: "resourceNames[] like customers/1234567890."},
						{Name: "Search (GAQL)", Method: "POST", Path: "/" + GoogleAdsAPIVersion + "/customers/{customerId}/googleAds:search", Description: "Run a Google Ads Query Language query. customerId has no dashes.",
							Body: map[string]any{"query": "SELECT campaign.id, campaign.name, metrics.clicks FROM campaign WHERE segments.date DURING LAST_7_DAYS"}, Response: "results[] and nextPageToken."},
					},
					Notes: []string{
						"Paths start with the API version. " + GoogleAdsAPIVersion + " is shown here, the newest when this release shipped; Google sunsets each version about a year after it ships, so check the Google Ads API sunset dates and use a supported one.",
						"Set the manager account ID on the connection when the account reaches customers through a manager (MCC) account.",
						"A test developer token reaches only test accounts.",
					},
				},
				Tests: []Fixture{
					{Name: "success", Status: 200, Body: `{"resourceNames":["customers/1"]}`, Expect: OK},
					{Name: "developer token not approved", Status: 403, Body: `{"error":{"code":403,"details":[{"errors":[{"errorCode":{"authorizationError":"DEVELOPER_TOKEN_NOT_APPROVED"}}]}]}}`, Expect: Invalid},
					{Name: "no access to customer", Status: 403, Body: `{"error":{"code":403,"status":"PERMISSION_DENIED","details":[{"errors":[{"errorCode":{"authorizationError":"USER_PERMISSION_DENIED"}}]}]}}`, Expect: OK},
					{Name: "rate limited", Status: 429, Body: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`, Expect: RateLimited},
					{Name: "expired token", Status: 401, Body: `{"error":{"code":401,"status":"UNAUTHENTICATED"}}`, Expect: Invalid},
				},
			},
		},
		{
			Slug: "microsoft-graph", Name: "Microsoft Graph", Category: "Productivity", Color: "#0078d4",
			Description: "Outlook mail and calendar, OneDrive, Teams and Microsoft 365 data through Microsoft Graph, as a signed in Microsoft account.",
			Website:     "https://entra.microsoft.com", DocsURL: "https://learn.microsoft.com/graph/auth-v2-user",
			Spec: Spec{
				BaseURL: "https://graph.microsoft.com",
				Auth: Auth{Type: AuthOAuth2, Name: "Authorization", OAuth: &OAuthConfig{
					AuthorizeURL: "https://login.microsoftonline.com/{tenant}/oauth2/v2.0/authorize",
					TokenURL:     "https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token",
					Tenant:       "common",
					Scopes:       []string{"openid", "email", "offline_access", "User.Read"},
					PKCE:         true,
					AccountField: "preferred_username",
				}},
				Defaults: Defaults{Strategy: "round_robin", CooldownSec: 60, MaxAttempts: 3},
				Quota:    QuotaInfo{Unit: "requests"},
				Rules: []Rule{
					{Name: "No access to this resource", Match: Match{Status: []int{403}, BodyContains: []string{"authorization_requestdenied", "accessdenied", "erroraccessdenied"}}, Outcome: OK},
				},
				UseStandardRules: true,
				Health:           &Health{Method: "GET", Path: "/v1.0/me"},
				Examples: []Example{
					{Name: "Me", Method: "GET", Path: "/v1.0/me", Safe: true},
				},
				Guide: &Guide{
					Summary: "Microsoft Graph as a signed in work, school or personal Microsoft account: profile, mail, calendar, files and Teams.",
					Endpoints: []GuideEndpoint{
						{Name: "Me", Method: "GET", Path: "/v1.0/me", Description: "The signed in user's profile."},
						{Name: "Messages", Method: "GET", Path: "/v1.0/me/messages", Description: "Recent mail. Needs the Mail.Read scope.", Params: []GuideParam{{Name: "$top", In: "query", Description: "Page size"}}},
						{Name: "Calendar events", Method: "GET", Path: "/v1.0/me/events", Description: "Calendar events. Needs Calendars.Read."},
					},
					Notes: []string{
						"Throttled calls answer 429 with Retry-After; the pool waits that long before using the account again.",
						"Add the delegated scopes you need (Mail.Read, Calendars.Read, Files.Read) on the service, then reconnect each account.",
						"Sign in goes through the common tenant (work, school and personal accounts) unless the service sets its own: organizations, consumers, or your tenant ID or domain for an app registered for one organization only. Set it with the OAuth client.",
					},
				},
				Tests: []Fixture{
					{Name: "success", Status: 200, Body: `{"id":"1"}`, Expect: OK},
					{Name: "throttled", Status: 429, Headers: map[string]string{"Retry-After": "10"}, Body: `{"error":{"code":"TooManyRequests"}}`, Expect: RateLimited},
					{Name: "access denied", Status: 403, Body: `{"error":{"code":"Authorization_RequestDenied","message":"Insufficient privileges"}}`, Expect: OK},
					{Name: "expired token", Status: 401, Body: `{"error":{"code":"InvalidAuthenticationToken"}}`, Expect: Invalid},
				},
			},
		},
	}
}
