#pragma once

// C++ client for Acnologia, a self-hosted API key pool.
//
// Lease mode: lease a key, call the provider yourself, report the outcome.
// Client::with_key wraps that loop and rotates keys for you.
// Proxy mode: send requests through Client::proxy; Acnologia injects a key
// and rotates on rate limits without exposing the secret.

#include "acnologia/client.hpp"
#include "acnologia/errors.hpp"
#include "acnologia/transport.hpp"
#include "acnologia/types.hpp"
