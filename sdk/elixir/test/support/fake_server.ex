defmodule Acnologia.FakeServer do
  @moduledoc false
  # A tiny HTTP/1.1 server on :gen_tcp for the tests. Each request is parsed,
  # sent to the owner process as {:fake_request, req}, and answered by the
  # handler: fn req -> {status, headers, body} end. A map or list body is
  # sent as JSON. Every answer closes the connection.

  defmodule Request do
    @moduledoc false
    defstruct method: "", path: "", query: %{}, raw_query: "", headers: %{}, body: ""

    def json(%__MODULE__{body: body}), do: JSON.decode!(body)
  end

  def start(handler) when is_function(handler, 1) do
    owner = self()

    {:ok, listen} =
      :gen_tcp.listen(0, [
        :binary,
        packet: :http_bin,
        active: false,
        reuseaddr: true,
        ip: {127, 0, 0, 1}
      ])

    {:ok, port} = :inet.port(listen)
    pid = spawn_link(fn -> accept_loop(listen, handler, owner) end)
    :ok = :gen_tcp.controlling_process(listen, pid)
    %{url: "http://127.0.0.1:#{port}", pid: pid, port: port}
  end

  def stop(%{pid: pid}) do
    Process.unlink(pid)
    Process.exit(pid, :kill)
    :ok
  end

  defp accept_loop(listen, handler, owner) do
    case :gen_tcp.accept(listen) do
      {:ok, sock} ->
        child = spawn(fn -> serve(sock, handler, owner) end)
        :gen_tcp.controlling_process(sock, child)
        send(child, :go)
        accept_loop(listen, handler, owner)

      {:error, _} ->
        :ok
    end
  end

  defp serve(sock, handler, owner) do
    receive do
      :go -> :ok
    end

    with {:ok, req} <- read_request(sock) do
      send(owner, {:fake_request, req})
      {status, headers, body} = handler.(req)

      body = if is_binary(body), do: body, else: JSON.encode!(body)

      headers =
        if Enum.any?(headers, fn {k, _} -> String.downcase(k) == "content-type" end),
          do: headers,
          else: [{"content-type", "application/json"} | headers]

      # With a Transfer-Encoding header the handler's body is sent as it is.
      length =
        if Enum.any?(headers, fn {k, _} -> String.downcase(k) == "transfer-encoding" end),
          do: "",
          else: "content-length: #{byte_size(body)}\r\n"

      head =
        Enum.map_join(headers, "", fn {k, v} -> "#{k}: #{v}\r\n" end) <>
          length <> "connection: close\r\n\r\n"

      :gen_tcp.send(sock, "HTTP/1.1 #{status} Status\r\n" <> head <> body)
    end

    :gen_tcp.close(sock)
  end

  defp read_request(sock) do
    with {:ok, {:http_request, method, {:abs_path, target}, _}} <- :gen_tcp.recv(sock, 0, 5000),
         {:ok, headers} <- read_headers(sock, %{}) do
      len = String.to_integer(Map.get(headers, "content-length", "0"))
      :inet.setopts(sock, packet: :raw)

      body =
        if len > 0 do
          {:ok, b} = :gen_tcp.recv(sock, len, 5000)
          b
        else
          ""
        end

      {path, raw_query} =
        case String.split(target, "?", parts: 2) do
          [p, q] -> {p, q}
          [p] -> {p, ""}
        end

      {:ok,
       %Request{
         method: to_string(method),
         path: path,
         raw_query: raw_query,
         query: URI.decode_query(raw_query),
         headers: headers,
         body: body
       }}
    end
  end

  defp read_headers(sock, acc) do
    case :gen_tcp.recv(sock, 0, 5000) do
      {:ok, {:http_header, _, name, _, value}} ->
        key = name |> to_string() |> String.downcase()
        read_headers(sock, Map.put(acc, key, value))

      {:ok, :http_eoh} ->
        {:ok, acc}

      other ->
        {:error, other}
    end
  end
end
