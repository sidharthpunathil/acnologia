defmodule Acnologia.HTTP.Raw do
  @moduledoc false
  # A minimal HTTP/1.1 client on :gen_tcp and :ssl: one request per
  # connection, Content-Length, chunked or read-until-close bodies, no
  # redirects and no retries. Used on OTP releases whose :httpc cannot turn
  # off its automatic retry of a 503 with Retry-After (see Acnologia.HTTP).

  @type headers :: [{String.t(), String.t()}]

  @spec request(atom(), String.t(), headers(), binary() | nil, keyword()) ::
          {:ok, {non_neg_integer(), headers(), binary()}} | {:error, term()}
  def request(method, url, headers, body, opts) do
    uri = URI.parse(url)
    https? = uri.scheme == "https"
    deadline = System.monotonic_time(:millisecond) + Keyword.fetch!(opts, :timeout)
    host = to_charlist(uri.host)
    sock_opts = [:binary, active: false, packet: :raw]

    {mod, connect_opts} =
      if https?,
        do: {:ssl, sock_opts ++ [server_name_indication: host] ++ Keyword.fetch!(opts, :ssl)},
        else: {:gen_tcp, sock_opts}

    with {:ok, sock} <-
           mod.connect(host, uri.port, connect_opts, Keyword.fetch!(opts, :connect_timeout)) do
      try do
        with :ok <- mod.send(sock, encode(method, uri, headers, body)) do
          read_response(mod, sock, method, deadline)
        end
      after
        mod.close(sock)
      end
    end
  end

  defp encode(method, uri, headers, body) do
    target = (uri.path || "/") <> if(uri.query, do: "?" <> uri.query, else: "")
    default_port = if uri.scheme == "https", do: 443, else: 80
    host = if uri.port == default_port, do: uri.host, else: "#{uri.host}:#{uri.port}"
    len = if body, do: [{"content-length", Integer.to_string(byte_size(body))}], else: []

    lines =
      [{"host", host}] ++ headers ++ len ++ [{"connection", "close"}]

    [
      method |> Atom.to_string() |> String.upcase(),
      " ",
      target,
      " HTTP/1.1\r\n",
      Enum.map(lines, fn {k, v} -> [k, ": ", v, "\r\n"] end),
      "\r\n",
      body || ""
    ]
  end

  defp read_response(mod, sock, method, deadline) do
    setopts(mod, sock, packet: :http_bin)

    with {:ok, status} <- read_status(mod, sock, deadline),
         {:ok, headers} <- read_headers(mod, sock, deadline, []) do
      if status in 100..199 do
        read_response(mod, sock, method, deadline)
      else
        setopts(mod, sock, packet: :raw)

        with {:ok, body} <- read_body(mod, sock, method, status, headers, deadline) do
          {:ok, {status, headers, body}}
        end
      end
    end
  end

  defp read_status(mod, sock, deadline) do
    case recv(mod, sock, 0, deadline) do
      {:ok, {:http_response, _, status, _}} -> {:ok, status}
      {:ok, other} -> {:error, {:bad_response, other}}
      err -> err
    end
  end

  defp read_headers(mod, sock, deadline, acc) do
    case recv(mod, sock, 0, deadline) do
      {:ok, {:http_header, _, name, _, value}} ->
        key = name |> to_string() |> String.downcase()
        read_headers(mod, sock, deadline, [{key, to_string(value)} | acc])

      {:ok, :http_eoh} ->
        {:ok, Enum.reverse(acc)}

      {:ok, other} ->
        {:error, {:bad_response, other}}

      err ->
        err
    end
  end

  defp read_body(_mod, _sock, :head, _status, _headers, _deadline), do: {:ok, ""}
  defp read_body(_mod, _sock, _method, status, _h, _d) when status in [204, 304], do: {:ok, ""}

  defp read_body(mod, sock, _method, _status, headers, deadline) do
    te = header(headers, "transfer-encoding")
    cl = header(headers, "content-length")

    cond do
      te && String.contains?(String.downcase(te), "chunked") ->
        read_chunks(mod, sock, deadline, [])

      cl ->
        case Integer.parse(String.trim(cl)) do
          {0, ""} -> {:ok, ""}
          {n, ""} when n > 0 -> recv(mod, sock, n, deadline)
          _ -> {:error, {:bad_response, :content_length}}
        end

      true ->
        read_to_close(mod, sock, deadline, [])
    end
  end

  defp read_chunks(mod, sock, deadline, acc) do
    setopts(mod, sock, packet: :line)

    with {:ok, line} <- recv(mod, sock, 0, deadline),
         {size, _} <- line |> String.trim() |> String.split(";") |> hd() |> Integer.parse(16) do
      if size == 0 do
        skip_trailers(mod, sock, deadline)
        {:ok, acc |> Enum.reverse() |> IO.iodata_to_binary()}
      else
        setopts(mod, sock, packet: :raw)

        with {:ok, <<data::binary-size(^size), _crlf::binary>>} <-
               recv(mod, sock, size + 2, deadline) do
          read_chunks(mod, sock, deadline, [data | acc])
        end
      end
    else
      {:error, _} = err -> err
      _ -> {:error, {:bad_response, :chunk}}
    end
  end

  defp skip_trailers(mod, sock, deadline) do
    case recv(mod, sock, 0, deadline) do
      {:ok, line} when line in ["\r\n", "\n"] -> :ok
      {:ok, _} -> skip_trailers(mod, sock, deadline)
      _ -> :ok
    end
  end

  defp read_to_close(mod, sock, deadline, acc) do
    case recv(mod, sock, 0, deadline) do
      {:ok, data} -> read_to_close(mod, sock, deadline, [data | acc])
      {:error, :closed} -> {:ok, acc |> Enum.reverse() |> IO.iodata_to_binary()}
      err -> err
    end
  end

  defp recv(mod, sock, n, deadline) do
    left = deadline - System.monotonic_time(:millisecond)
    if left <= 0, do: {:error, :timeout}, else: mod.recv(sock, n, left)
  end

  defp setopts(:ssl, sock, opts), do: :ssl.setopts(sock, opts)
  defp setopts(:gen_tcp, sock, opts), do: :inet.setopts(sock, opts)

  defp header(headers, name) do
    Enum.find_value(headers, fn {k, v} -> if k == name, do: v end)
  end
end
