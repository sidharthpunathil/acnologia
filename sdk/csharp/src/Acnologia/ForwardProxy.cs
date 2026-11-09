using System.Net;
using System.Net.Security;
using System.Security.Cryptography.X509Certificates;

namespace Acnologia;

public sealed partial class AcnologiaClient
{
    /// <summary>The file name the forward proxy's CA is saved under by default.</summary>
    public const string DefaultCAPath = "acnologia-ca.pem";

    /// <summary>
    /// How to use the forward proxy, from <c>GET /api/v1/proxy</c>: the proxy address, the CA,
    /// the hosts it routes and the environment an app sets. It never holds the token.
    /// </summary>
    public async Task<ForwardProxy> ForwardProxyAsync(CancellationToken cancellationToken = default) =>
        await SendJsonAsync<ForwardProxy>(HttpMethod.Get, "/api/v1/proxy", null, cancellationToken).ConfigureAwait(false)
        ?? throw new AcnologiaException("acnologia: empty forward proxy response");

    /// <summary>The forward proxy's CA certificate as PEM text. Public: sent without the token.</summary>
    public Task<string> ForwardProxyCAAsync(CancellationToken cancellationToken = default) =>
        SendTextAsync(HttpMethod.Get, "/api/v1/proxy/ca.pem", null, "application/x-pem-file", false, cancellationToken);

    /// <summary>Downloads the forward proxy's CA certificate to <paramref name="path"/> and returns its full path.</summary>
    public async Task<string> SaveForwardProxyCAAsync(string path = DefaultCAPath, CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(path);
        var pem = await ForwardProxyCAAsync(cancellationToken).ConfigureAwait(false);
        await File.WriteAllTextAsync(path, pem, cancellationToken).ConfigureAwait(false);
        return Path.GetFullPath(path);
    }

    /// <summary>
    /// The forward proxy address with user <c>app</c> and this client's token as the password,
    /// for example <c>http://app:acn_...@host:8888</c>. The result contains the token: do not log it.
    /// </summary>
    public async Task<string> ForwardProxyUrlAsync(CancellationToken cancellationToken = default)
    {
        var fp = await ForwardProxyAsync(cancellationToken).ConfigureAwait(false);
        return ProxyUrlWithToken(fp.ProxyUrl, Endpoint.Token);
    }

    /// <summary>
    /// The environment that sends an app's HTTP through the forward proxy: <c>HTTPS_PROXY</c> and
    /// <c>HTTP_PROXY</c> (see <see cref="ForwardProxyUrlAsync"/>), <c>NODE_USE_ENV_PROXY=1</c>, and
    /// <c>NODE_EXTRA_CA_CERTS</c>, <c>REQUESTS_CA_BUNDLE</c> and <c>SSL_CERT_FILE</c> set to
    /// <paramref name="caPath"/>. The values contain the token: do not log them.
    /// </summary>
    public async Task<IReadOnlyDictionary<string, string>> ForwardProxyEnvAsync(string caPath = DefaultCAPath,
        CancellationToken cancellationToken = default)
    {
        ArgumentException.ThrowIfNullOrEmpty(caPath);
        var url = await ForwardProxyUrlAsync(cancellationToken).ConfigureAwait(false);
        return new Dictionary<string, string>
        {
            ["HTTPS_PROXY"] = url,
            ["HTTP_PROXY"] = url,
            ["NODE_USE_ENV_PROXY"] = "1",
            ["NODE_EXTRA_CA_CERTS"] = caPath,
            ["REQUESTS_CA_BUNDLE"] = caPath,
            ["SSL_CERT_FILE"] = caPath,
        };
    }

    /// <summary>
    /// An <see cref="HttpClientHandler"/> that sends every request through the forward proxy (with
    /// this client's token as the proxy password) and trusts the Acnologia CA next to the system
    /// roots. Wrap it in an <see cref="HttpClient"/> and call provider URLs as they are; Acnologia
    /// injects the pool key. The caller disposes it.
    /// </summary>
    public async Task<HttpClientHandler> CreateForwardProxyHandlerAsync(CancellationToken cancellationToken = default)
    {
        var fp = await ForwardProxyAsync(cancellationToken).ConfigureAwait(false);
        var pem = await ForwardProxyCAAsync(cancellationToken).ConfigureAwait(false);
        var ca = X509Certificate2.CreateFromPem(pem);
        return new HttpClientHandler
        {
            Proxy = new WebProxy(StripUserInfo(fp.ProxyUrl)) { Credentials = new NetworkCredential("app", Endpoint.Token) },
            UseProxy = true,
            AllowAutoRedirect = false,
            ServerCertificateCustomValidationCallback = (_, cert, chain, errors) => TrustsCA(ca, cert, chain, errors),
        };
    }

    /// <summary>Accepts a certificate the system trusts, or one that chains to <paramref name="ca"/> and matches the host.</summary>
    internal static bool TrustsCA(X509Certificate2 ca, X509Certificate2? cert, X509Chain? chain, SslPolicyErrors errors)
    {
        if (errors == SslPolicyErrors.None) return true;
        if (cert is null || (errors & ~SslPolicyErrors.RemoteCertificateChainErrors) != 0) return false;
        using var custom = new X509Chain();
        custom.ChainPolicy.TrustMode = X509ChainTrustMode.CustomRootTrust;
        custom.ChainPolicy.CustomTrustStore.Add(ca);
        custom.ChainPolicy.RevocationMode = X509RevocationMode.NoCheck;
        if (chain is not null) custom.ChainPolicy.ExtraStore.AddRange(chain.ChainPolicy.ExtraStore);
        return custom.Build(cert);
    }

    internal static string ProxyUrlWithToken(string proxyUrl, string token)
    {
        var bare = StripUserInfo(proxyUrl);
        var i = bare.IndexOf("://", StringComparison.Ordinal);
        if (i < 0) throw new AcnologiaException("acnologia: forward proxy URL is not absolute: " + bare);
        return bare[..(i + 3)] + "app:" + Uri.EscapeDataString(token) + "@" + bare[(i + 3)..];
    }

    private static string StripUserInfo(string url)
    {
        var i = url.IndexOf("://", StringComparison.Ordinal);
        if (i < 0) return url;
        var rest = url[(i + 3)..];
        var slash = rest.IndexOf('/');
        var at = (slash < 0 ? rest : rest[..slash]).LastIndexOf('@');
        return at >= 0 ? url[..(i + 3)] + rest[(at + 1)..] : url;
    }
}
