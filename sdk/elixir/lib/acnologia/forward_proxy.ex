defmodule Acnologia.ForwardProxy do
  @moduledoc """
  How to use the forward proxy, from `GET /api/v1/proxy`. Returned by
  `Acnologia.forward_proxy/1`.

    * `proxy_url`: the proxy URL, without credentials
    * `proxy_url_with_token`: the same with user `app` and an `<app token>`
      placeholder
    * `ca_url`, `ca_fingerprint`: the CA certificate an app must trust
    * `passthrough`: whether HTTPS to hosts that are no service is tunneled
    * `hosts`: `[%{host:, service:, name:, path:}]`, the provider hosts of the
      services the token may use
    * `env`: the environment to set, with the token placeholder
    * `service_header`: the request header that picks a service when several
      match one host

  Nothing here holds the app token, so it is safe to log.
  `Acnologia.forward_proxy_url/1` and `Acnologia.forward_proxy_env/2` fill
  the token in.
  """

  defstruct proxy_url: "",
            proxy_url_with_token: "",
            ca_url: "",
            ca_fingerprint: "",
            passthrough: false,
            hosts: [],
            env: %{},
            service_header: ""

  @type host :: %{host: String.t(), service: String.t(), name: String.t(), path: String.t()}

  @type t :: %__MODULE__{
          proxy_url: String.t(),
          proxy_url_with_token: String.t(),
          ca_url: String.t(),
          ca_fingerprint: String.t(),
          passthrough: boolean(),
          hosts: [host()],
          env: %{optional(String.t()) => String.t()},
          service_header: String.t()
        }

  @doc false
  @spec from_json(map()) :: t()
  def from_json(data) when is_map(data) do
    env = if is_map(data["env"]), do: data["env"], else: %{}

    %__MODULE__{
      proxy_url: str(data["proxy_url"]),
      proxy_url_with_token: str(data["proxy_url_with_token"]),
      ca_url: str(data["ca_url"]),
      ca_fingerprint: str(data["ca_fingerprint"]),
      passthrough: data["passthrough"] == true,
      hosts:
        for h <- List.wrap(data["hosts"]), is_map(h) do
          %{
            host: str(h["host"]),
            service: str(h["service"]),
            name: str(h["name"]),
            path: str(h["path"])
          }
        end,
      env: Map.new(env, fn {k, v} -> {to_string(k), to_string(v)} end),
      service_header: str(data["service_header"])
    }
  end

  defp str(v) when is_binary(v), do: v
  defp str(_), do: ""
end
