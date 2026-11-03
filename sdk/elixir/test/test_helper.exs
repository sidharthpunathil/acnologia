# ACNOLOGIA_TEST_HTTP=raw or =httpc runs the suite on one HTTP adapter.
# Left unset, the client picks the adapter for this OTP release.
case System.get_env("ACNOLOGIA_TEST_HTTP") do
  "raw" -> Application.put_env(:acnologia, :http_adapter, :raw)
  "httpc" -> Application.put_env(:acnologia, :http_adapter, :httpc)
  _ -> :ok
end

IO.puts("HTTP adapter: #{Acnologia.HTTP.adapter()}")
ExUnit.start()
