{-# LANGUAGE LambdaCase #-}
{-# LANGUAGE OverloadedStrings #-}
{-# LANGUAGE ScopedTypeVariables #-}

-- | Tests against a fake Acnologia started in process with warp.
module Main (main) where

import Acnologia
import Control.Exception (displayException, try)
import Data.Aeson (Value (..), object, (.=))
import qualified Data.Aeson as A
import qualified Data.Aeson.Key as AK
import qualified Data.Aeson.KeyMap as KM
import qualified Data.ByteString as BS
import qualified Data.ByteString.Lazy as LBS
import qualified Data.CaseInsensitive as CI
import Data.IORef
import qualified Data.Map.Strict as Map
import Data.Text (Text)
import qualified Data.Text as T
import qualified Data.Text.Encoding as TE
import Data.Time (UTCTime (..), addUTCTime, defaultTimeLocale, formatTime, fromGregorian, getCurrentTime)
import qualified Network.HTTP.Client as H
import Network.HTTP.Types (RequestHeaders, ResponseHeaders, Status, mkStatus, parseQuery, urlEncode)
import qualified Network.Wai as W
import qualified Network.Wai.Handler.Warp as Warp
import System.Environment (setEnv, unsetEnv)
import Test.Tasty
import Test.Tasty.HUnit

-- ---------------------------------------------------------------------------
-- Fake server

token, access, secret :: Text
token = "acn_test_token_123"
access = "access-password-123"
secret = "sk/test+secret=1"

-- | One request the fake server saw.
data Seen = Seen
  { seenMethod :: BS.ByteString
  , seenPath :: [Text]
  , seenQuery :: BS.ByteString
  , seenHeaders :: RequestHeaders
  , seenBody :: LBS.ByteString
  }

data Fake = Fake
  { fakeSeen :: IORef [Seen]
  , fakeIdem :: IORef (Map.Map BS.ByteString LBS.ByteString)
  , fakeCalls :: IORef Int
  }

jsonResp :: Status -> ResponseHeaders -> Value -> W.Response
jsonResp st hdrs v = W.responseLBS st (("Content-Type", "application/json") : hdrs) (A.encode v)

errBody :: Text -> Text -> [(AK.Key, Value)] -> Value
errBody code msg extra = object (("error" .= object ["code" .= code, "message" .= msg]) : extra)

leaseJson :: Text -> Text -> Text -> Text -> Int -> Value
leaseJson lid authT authN prefix keyId =
  object
    [ "lease_id" .= lid
    , "key_id" .= keyId
    , "service" .= ("agi-labs" :: Text)
    , "secret" .= secret
    , "auth" .= object ["type" .= authT, "name" .= authN, "prefix" .= prefix]
    , "base_url" .= ("https://agi-labs.example/" :: Text)
    , "expires_at" .= ("2026-11-04T12:00:00.123456789Z" :: Text)
    , "exclusive" .= True
    ]

app :: Fake -> W.Application
app fake req respond = do
  body <- W.strictRequestBody req
  let seen = Seen (W.requestMethod req) (W.pathInfo req) (W.rawQueryString req) (W.requestHeaders req) body
  modifyIORef' (fakeSeen fake) (seen :)
  let bodyObj = case A.decode body of
        Just (Object o) -> o
        _ -> KM.empty
      bodyText k = case KM.lookup k bodyObj of
        Just (String s) -> s
        _ -> ""
  respond =<< case W.pathInfo req of
    ["api", "v1", "me"] ->
      pure . jsonResp (mkStatus 200 "OK") [] $
        object
          [ "app" .= object ["id" .= (7 :: Int), "name" .= ("Website" :: Text)]
          , "limits" .= object ["daily_requests" .= (500 :: Int), "monthly_requests" .= Null, "daily_budget" .= Null, "monthly_budget" .= (20 :: Double)]
          , "usage"
              .= object
                [ "today_requests" .= (3 :: Int)
                , "today_cost" .= (0.5 :: Double)
                , "month_requests" .= (9 :: Int)
                , "month_cost" .= (1.25 :: Double)
                , "last_used_at" .= ("2026-11-04T10:00:00Z" :: Text)
                , "limit_hit" .= Null
                ]
          , "limit_hit" .= Null
          , "services" .= [object ["slug" .= ("agi-labs" :: Text), "name" .= ("AGI Labs" :: Text), "provider" .= ("custom" :: Text), "category" .= ("ai" :: Text), "available" .= (3 :: Int), "logo_url" .= ("data:image/png;base64,AAAA" :: Text)]]
          , "share" .= Null
          ]
    ["api", "v1", "services"] ->
      pure . jsonResp (mkStatus 200 "OK") [] $
        object ["services" .= [object ["slug" .= ("agi-labs" :: Text), "available" .= True, "base_url" .= ("https://agi-labs.example" :: Text), "auth" .= object ["type" .= ("bearer" :: Text), "name" .= ("" :: Text)], "guide_url" .= ("http://acn.test/api/v1/services/agi-labs/guide" :: Text)]]]
    ["api", "v1", "lease"] -> pure $ case bodyText "service" of
      "agi-labs" -> jsonResp (mkStatus 200 "OK") [] (leaseJson "lease-1" "header" "X-Api-Key" "" 11)
      "agi-labs-query" -> jsonResp (mkStatus 200 "OK") [] (leaseJson "lease-q" "query" "key" "" 12)
      "agi-labs-bearer" -> jsonResp (mkStatus 200 "OK") [] (leaseJson "lease-b" "bearer" "" "" 13)
      "busy" -> jsonResp (mkStatus 503 "Service Unavailable") [] (errBody "all_keys_busy" "every key is busy" ["retry_after_sec" .= (2 :: Int)])
      "drained" -> jsonResp (mkStatus 503 "Service Unavailable") [("Retry-After", "30")] (errBody "no_key_available" "no key" [])
      "plain503" -> W.responseLBS (mkStatus 503 "Service Unavailable") [] "busy"
      "shared" -> jsonResp (mkStatus 403 "Forbidden") [] (errBody "lease_not_allowed" "proxy only" [])
      _ -> jsonResp (mkStatus 404 "Not Found") [] (errBody "unknown_service" "no such service" [])
    ["api", "v1", "leases", lid, "renew"]
      | lid == "gone" -> pure (jsonResp (mkStatus 404 "Not Found") [] (errBody "lease_not_found" "lease is not open" []))
      | otherwise -> pure (jsonResp (mkStatus 200 "OK") [] (object ["ok" .= True, "expires_at" .= ("2026-11-04T13:00:00Z" :: Text)]))
    ["api", "v1", "leases", lid, "report"]
      | lid == "done" -> pure (jsonResp (mkStatus 409 "Conflict") [] (errBody "report_failed" "lease already reported" []))
      | KM.lookup "rotate" bodyObj == Just (Bool True) ->
          pure (jsonResp (mkStatus 200 "OK") [] (object ["ok" .= True, "next" .= leaseJson (lid <> "-next") "header" "X-Api-Key" "" 21]))
      | otherwise -> pure (jsonResp (mkStatus 200 "OK") [] (object ["ok" .= True, "next" .= Null]))
    -- Acnologia's own errors: no X-Acnologia-Service header.
    ("proxy" : "err" : st : code : _) -> do
      now <- getCurrentTime
      let status = read (T.unpack st)
          extra = case code of
            "app_limit_reached" -> ["limit" .= ("daily_requests" :: Text), "reset_at" .= ("2026-11-05T00:00:00Z" :: Text), "retry_after_sec" .= (3600 :: Int)]
            "service_unavailable" -> ["retry_after_sec" .= (0 :: Int)]
            _ -> []
          hdrs = case code of
            "service_unavailable" -> [("Retry-After", "45")]
            "dated" -> [("Retry-After", TE.encodeUtf8 (T.pack (formatTime defaultTimeLocale "%a, %d %b %Y %H:%M:%S GMT" (addUTCTime 120 now))))]
            _ -> []
          code' = if code == "dated" then "no_key_available" else code
      pure $
        if code == "none"
          then W.responseLBS (mkStatus status "") [] "plain failure"
          else jsonResp (mkStatus status "") hdrs (errBody code' ("failed for " <> token <> " and " <> access) extra)
    ("proxy" : "share-header" : _) ->
      pure (jsonResp (mkStatus 403 "Forbidden") [("X-Acnologia-Share", "paused")] (object ["error" .= ("paused" :: Text)]))
    ("proxy" : "agi-labs" : rest) -> case rest of
      ["v1", "limited"] ->
        pure (jsonResp (mkStatus 429 "Too Many Requests") [("X-Acnologia-Service", "agi-labs"), ("X-Acnologia-Key-Id", "4")] (errBody "rate_limited" "slow down" []))
      ["v1", "moved"] ->
        pure (W.responseLBS (mkStatus 302 "Found") [("Location", "http://127.0.0.1:1/elsewhere"), ("X-Acnologia-Service", "agi-labs")] "")
      _ -> case lookup "Idempotency-Key" (W.requestHeaders req) of
        Just k -> do
          stored <- Map.lookup k <$> readIORef (fakeIdem fake)
          case stored of
            Just b -> pure (W.responseLBS (mkStatus 201 "Created") [("Idempotent-Replayed", "true"), ("X-Acnologia-Cache", "replay"), ("X-Acnologia-Key-Id", "4")] b)
            Nothing -> do
              n <- atomicModifyIORef' (fakeCalls fake) (\c -> (c + 1, c + 1))
              let b = A.encode (object ["call" .= n])
              modifyIORef' (fakeIdem fake) (Map.insert k b)
              pure (W.responseLBS (mkStatus 201 "Created") [("X-Acnologia-Service", "agi-labs"), ("X-Acnologia-Key-Id", "4"), ("X-Acnologia-Attempts", "1")] b)
        Nothing ->
          pure $
            W.responseLBS
              (mkStatus 200 "OK")
              [("Content-Type", "application/json"), ("X-Acnologia-Key-Id", "4"), ("X-Acnologia-Attempts", "2"), ("X-Acnologia-Service", "agi-labs"), ("X-Acnologia-Cache", "miss")]
              (A.encode (object ["echo" .= TE.decodeUtf8 (LBS.toStrict body)]))
    ["api", "v1", "services", "agi-labs", "guide"]
      | lookup "Accept" (W.requestHeaders req) == Just "text/markdown" ->
          pure (W.responseLBS (mkStatus 200 "OK") [("Content-Type", "text/markdown; charset=utf-8")] "# AGI Labs\n\nCall /v1/chat.\n")
      | otherwise ->
          pure . jsonResp (mkStatus 200 "OK") [] $
            object ["markdown" .= ("# AGI Labs\n" :: Text), "generated" .= True, "guide" .= object ["endpoints" .= [object ["path" .= ("/v1/chat" :: Text)]]]]
    ["api", "v1", "services", "secret", "guide"] ->
      pure (jsonResp (mkStatus 403 "Forbidden") [] (errBody "service_not_allowed" "this app cannot use service secret" []))
    ["api", "v1", "capabilities"] ->
      pure . jsonResp (mkStatus 200 "OK") [] $
        object
          [ "capabilities"
              .= [ object
                     [ "slug" .= ("maps-search" :: Text)
                     , "name" .= ("Maps search" :: Text)
                     , "description" .= ("Find places" :: Text)
                     , "strategy" .= ("priority" :: Text)
                     , "params" .= object ["query" .= object ["type" .= ("string" :: Text), "required" .= True]]
                     , "routes" .= [object ["service" .= ("omkar-cloud" :: Text), "mapped" .= True], object ["service" .= ("serpapi" :: Text), "mapped" .= False]]
                     , "url" .= ("http://acn.test/proxy/maps-search" :: Text)
                     ]
                 ]
          ]
    ["proxy", "maps-search"] ->
      pure $
        jsonResp
          (mkStatus 200 "OK")
          ( [ ("X-Acnologia-Capability", "maps-search")
            , ("X-Acnologia-Service", "serpapi")
            , ("X-Acnologia-Tried", "omkar-cloud=503, serpapi=200")
            , ("X-Acnologia-Key-Id", "9")
            , ("X-Acnologia-Attempts", "1")
            ]
              <> [("X-Acnologia-Cache", "miss") | lookup "Cache-Control" (W.requestHeaders req) == Just "no-cache"]
          )
          (object ["items" .= ([] :: [Value]), "count" .= (0 :: Int)])
    ["proxy", "no-route"] ->
      pure (jsonResp (mkStatus 503 "Service Unavailable") [] (errBody "no_route" "no route" []))
    ["api", "v1", "proxy"] ->
      pure . jsonResp (mkStatus 200 "OK") [] $
        object
          [ "proxy_url" .= ("http://acn.test:8888" :: Text)
          , "proxy_url_with_token" .= ("http://app:%3Capp%20token%3E@acn.test:8888" :: Text)
          , "ca_url" .= ("http://acn.test/api/v1/proxy/ca.pem" :: Text)
          , "ca_fingerprint" .= ("AB:CD" :: Text)
          , "passthrough" .= True
          , "hosts" .= [object ["host" .= ("serpapi.com" :: Text), "service" .= ("serpapi" :: Text), "name" .= ("SerpAPI" :: Text), "path" .= ("" :: Text)]]
          , "env" .= object ["HTTPS_PROXY" .= ("http://app:%3Capp%20token%3E@acn.test:8888" :: Text), "NODE_USE_ENV_PROXY" .= ("1" :: Text)]
          , "service_header" .= ("X-Acnologia-Service" :: Text)
          ]
    ["api", "v1", "proxy", "ca.pem"] ->
      pure (W.responseLBS (mkStatus 200 "OK") [("Content-Type", "application/x-pem-file")] (LBS.fromStrict (TE.encodeUtf8 pemText)))
    _ -> pure (jsonResp (mkStatus 404 "Not Found") [] (errBody "not_found" "not found" []))

pemText :: Text
pemText = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"

withFake :: ((Fake, Text) -> IO a) -> IO a
withFake act = do
  fake <- Fake <$> newIORef [] <*> newIORef Map.empty <*> newIORef 0
  Warp.testWithApplication (pure (app fake)) $ \port ->
    act (fake, "http://127.0.0.1:" <> T.pack (show port))

client :: Text -> IO Client
client url = newClientOrThrow defaultConfig {configBaseUrl = Just url, configToken = Just token}

lastSeen :: Fake -> IO Seen
lastSeen f = readIORef (fakeSeen f) >>= \case
  (s : _) -> pure s
  [] -> assertFailure "no request seen" >> error "unreachable"

seenHeader :: Seen -> BS.ByteString -> Maybe BS.ByteString
seenHeader s k = lookup (CI.mk k) (seenHeaders s)

seenJson :: Seen -> KM.KeyMap Value
seenJson s = case A.decode (seenBody s) of
  Just (Object o) -> o
  _ -> KM.empty

expectLeft :: Show a => Either AcnologiaError a -> IO AcnologiaError
expectLeft = \case
  Left e -> pure e
  Right v -> assertFailure ("expected an error, got " <> show v) >> error "unreachable"

expectRight :: Either AcnologiaError a -> IO a
expectRight = \case
  Right v -> pure v
  Left e -> assertFailure ("unexpected error: " <> show e) >> error "unreachable"

-- ---------------------------------------------------------------------------
-- Tests

main :: IO ()
main = withFake $ \(fake, url) -> defaultMain (tests fake url)

tests :: Fake -> Text -> TestTree
tests fake url =
  testGroup
    "acnologia"
    [ testGroup "errors" (errorMappings url <> [retryAfterTests url, otherErrors fake url])
    , proxyTests fake url
    , leaseTests fake url
    , applyTests
    , meTests fake url
    , envTests fake url
    , scrubTests fake url
    , v2Tests fake url
    ]

-- Every code the docs list, mapped through a proxy call to the fake.
errorMappings :: Text -> [TestTree]
errorMappings url =
  [ check 401 "unauthenticated" (\case Unauthenticated i -> infoStatus i == 401; _ -> False)
  , check 403 "unauthenticated" (\case Unauthenticated i -> infoStatus i == 403; _ -> False)
  , check 403 "service_not_allowed" (\case ServiceNotAllowed _ -> True; _ -> False)
  , check 403 "proxy_not_allowed" (\case ProxyNotAllowed _ -> True; _ -> False)
  , check 403 "lease_not_allowed" (\case LeaseNotAllowed _ -> True; _ -> False)
  , check 503 "no_key_available" (\case NoKeyAvailable _ _ -> True; _ -> False)
  , check 503 "all_keys_busy" (\case AllKeysBusy _ _ -> True; _ -> False)
  , check 503 "service_unavailable" (\case ServiceUnavailable _ (Just 45) -> True; _ -> False)
  , check 429 "app_limit_reached" $ \case
      AppLimitReached _ "daily_requests" (Just t) (Just 3600) -> t == UTCTime (fromGregorian 2026 11 5) 0
      _ -> False
  , check 403 "share_revoked" (\case ShareRevoked _ -> True; _ -> False)
  , check 403 "share_paused" (\case SharePaused _ -> True; _ -> False)
  , check 403 "share_expired" (\case ShareExpired _ -> True; _ -> False)
  , check 404 "lease_not_found" (\case LeaseNotFound _ -> True; _ -> False)
  , check 409 "report_failed" (\case ReportFailed i -> infoStatus i == 409; _ -> False)
  , check 410 "report_failed" (\case ReportFailed i -> infoStatus i == 410; _ -> False)
  , check 502 "upstream_unreachable" (\case UpstreamUnreachable _ -> True; _ -> False)
  , check 502 "upstream_blocked" (\case UpstreamBlocked _ -> True; _ -> False)
  , check 508 "loop_detected" (\case LoopDetected _ -> True; _ -> False)
  , check 400 "bad_path" (\case BadPath _ -> True; _ -> False)
  , check 413 "body_too_large" (\case BodyTooLarge _ -> True; _ -> False)
  , check 404 "unknown_service" (\case ApiError i -> infoCode i == "unknown_service"; _ -> False)
  , check 405 "method_not_allowed" (\case ApiError i -> infoStatus i == 405; _ -> False)
  , testCase "503 without a code on an API call" $ do
      c <- client url
      e <- expectLeft =<< lease c "plain503" defaultLeaseOptions
      e @?= NoKeyAvailable (ErrorInfo 503 "no_key_available" "Service Unavailable") Nothing
  , testCase "503 without a code or Acnologia headers on the proxy is left to the caller" $ do
      c <- client url
      r <- expectRight =<< proxyGet c "err" "503/none" []
      responseStatus r @?= 503
  ]
  where
    check :: Int -> Text -> (AcnologiaError -> Bool) -> TestTree
    check st code ok = testCase (show st <> " " <> T.unpack code) $ do
      c <- client url
      e <- expectLeft =<< proxyGet c "err" (T.pack (show st) <> "/" <> code) []
      assertBool ("unexpected mapping: " <> show e) (ok e)
      errorStatus e @?= st

retryAfterTests :: Text -> TestTree
retryAfterTests url =
  testGroup
    "retry after"
    [ testCase "body retry_after_sec wins" $ do
        c <- client url
        e <- expectLeft =<< lease c "busy" defaultLeaseOptions
        errorRetryAfter e @?= Just 2
        errorCode e @?= "all_keys_busy"
    , testCase "Retry-After seconds header" $ do
        c <- client url
        e <- expectLeft =<< lease c "drained" defaultLeaseOptions
        e @?= NoKeyAvailable (ErrorInfo 503 "no_key_available" "no key") (Just 30)
    , testCase "Retry-After HTTP date header" $ do
        c <- client url
        e <- expectLeft =<< proxyGet c "err" "503/dated" []
        case errorRetryAfter e of
          Just s -> assertBool ("retry after " <> show s) (s > 100 && s <= 121)
          Nothing -> assertFailure "no retry after"
    , testCase "zero in the body falls back to the header" $ do
        c <- client url
        e <- expectLeft =<< proxyGet c "err" "503/service_unavailable" []
        errorRetryAfter e @?= Just 45
    ]

otherErrors :: Fake -> Text -> TestTree
otherErrors _ url =
  testGroup
    "other"
    [ testCase "share status header without a code" $ do
        c <- client url
        e <- expectLeft =<< proxyGet c "share-header" "x" []
        assertBool (show e) (case e of SharePaused _ -> True; _ -> False)
    , testCase "connection failure is HttpFailure" $ do
        c <- client "http://127.0.0.1:1"
        e <- expectLeft =<< me c
        assertBool (show e) (case e of HttpFailure _ -> True; _ -> False)
    , testCase "throwing variant throws AcnologiaError" $ do
        c <- client url
        r <- try (leaseOrThrow c "shared" defaultLeaseOptions)
        case r of
          Left (LeaseNotAllowed i) -> infoMessage i @?= "proxy only"
          other -> assertFailure (show other)
    , testCase "wait outside 0 to 60 is rejected locally" $ do
        c <- client url
        e <- expectLeft =<< lease c "agi-labs" defaultLeaseOptions {leaseWaitSec = Just 61}
        assertBool (show e) (case e of InvalidInput _ -> True; _ -> False)
    ]

proxyTests :: Fake -> Text -> TestTree
proxyTests fake url =
  testGroup
    "proxy"
    [ testCase "sends method, path, query, headers and body" $ do
        c <- client url
        let req =
              withJsonBody
                (object ["prompt" .= ("hi" :: Text)])
                (proxyRequest "POST" "/v1/chat")
                  { proxyQuery = [("model", "small one")]
                  , proxyHeaders = [("X-Trace", "abc"), ("Authorization", "Bearer user-supplied")]
                  }
        r <- expectRight =<< proxy c "agi-labs" req
        responseStatus r @?= 200
        acnKeyId (responseAcnologia r) @?= Just 4
        acnAttempts (responseAcnologia r) @?= Just 2
        acnService (responseAcnologia r) @?= Just "agi-labs"
        acnCache (responseAcnologia r) @?= Just "miss"
        Map.lookup "x-acnologia-key-id" (acnAll (responseAcnologia r)) @?= Just "4"
        lookup "Content-Type" (responseHeaders r) @?= Just "application/json"
        s <- lastSeen fake
        seenMethod s @?= "POST"
        seenPath s @?= ["proxy", "agi-labs", "v1", "chat"]
        parseQuery (seenQuery s) @?= [("model", Just "small one")]
        seenHeader s "X-Trace" @?= Just "abc"
        seenHeader s "Authorization" @?= Just ("Bearer " <> TE.encodeUtf8 token)
        seenHeader s "Content-Type" @?= Just "application/json"
        seenHeader s "X-Acnologia-Access" @?= Nothing
        seenJson s @?= KM.fromList [("prompt", String "hi")]
        A.decode (responseBody r) @?= Just (object ["echo" .= ("{\"prompt\":\"hi\"}" :: Text)])
    , testCase "provider errors come back as Right" $ do
        c <- client url
        r <- expectRight =<< proxyGet c "agi-labs" "v1/limited" []
        responseStatus r @?= 429
    , testCase "redirects are not followed" $ do
        c <- client url
        r <- expectRight =<< proxyGet c "agi-labs" "v1/moved" []
        responseStatus r @?= 302
    , testCase "idempotency key is sent and a replay is flagged" $ do
        c <- client url
        let req = (withJsonBody (object ["n" .= (1 :: Int)]) (proxyRequest "POST" "v1/chat")) {proxyIdempotencyKey = Just "order-42"}
        r1 <- expectRight =<< proxy c "agi-labs" req
        s <- lastSeen fake
        seenHeader s "Idempotency-Key" @?= Just "order-42"
        acnReplayed (responseAcnologia r1) @?= False
        r2 <- expectRight =<< proxy c "agi-labs" req
        acnReplayed (responseAcnologia r2) @?= True
        acnCache (responseAcnologia r2) @?= Just "replay"
        responseBody r2 @?= responseBody r1
        responseStatus r2 @?= 201
    ]

leaseTests :: Fake -> Text -> TestTree
leaseTests fake url =
  testGroup
    "lease"
    [ testCase "lease sends options and decodes the lease" $ do
        c <- client url
        l <- expectRight =<< lease c "agi-labs" (LeaseOptions [1, 2] (Just 90) (Just 5) True)
        leaseId l @?= "lease-1"
        leaseKeyId l @?= 11
        leaseSecret l @?= secret
        leaseBaseUrl l @?= "https://agi-labs.example"
        leaseExclusive l @?= True
        leaseAuth l @?= Auth AuthHeader "X-Api-Key" ""
        s <- lastSeen fake
        seenPath s @?= ["api", "v1", "lease"]
        seenJson s
          @?= KM.fromList
            [ ("service", String "agi-labs")
            , ("exclude_key_ids", A.toJSON [1 :: Int, 2])
            , ("ttl_sec", Number 90)
            , ("wait_sec", Number 5)
            , ("exclusive", Bool True)
            ]
    , testCase "default options send only the service" $ do
        c <- client url
        _ <- expectRight =<< lease c "agi-labs" defaultLeaseOptions
        s <- lastSeen fake
        seenJson s @?= KM.fromList [("service", String "agi-labs")]
    , testCase "renew updates the expiry" $ do
        c <- client url
        l <- expectRight =<< lease c "agi-labs" defaultLeaseOptions
        l' <- expectRight =<< renew c l (Just 600)
        leaseExpiresAt l' @?= Just (UTCTime (fromGregorian 2026 11 4) (13 * 3600))
        s <- lastSeen fake
        seenPath s @?= ["api", "v1", "leases", "lease-1", "renew"]
        seenJson s @?= KM.fromList [("ttl_sec", Number 600)]
    , testCase "renew of a closed lease is LeaseNotFound" $ do
        c <- client url
        l <- expectRight =<< lease c "agi-labs" defaultLeaseOptions
        e <- expectLeft =<< renew c l {leaseId = "gone"} Nothing
        assertBool (show e) (case e of LeaseNotFound _ -> True; _ -> False)
    , testCase "report sends every field" $ do
        c <- client url
        l <- expectRight =<< lease c "agi-labs" defaultLeaseOptions
        n <- expectRight =<< report c l (Report OutcomeRateLimited (Just 429) (Just 1.2) (Just "slow down") False)
        n @?= Nothing
        s <- lastSeen fake
        seenPath s @?= ["api", "v1", "leases", "lease-1", "report"]
        seenJson s
          @?= KM.fromList
            [ ("outcome", String "rate_limited")
            , ("status_code", Number 429)
            , ("retry_after_sec", Number 2)
            , ("message", String "slow down")
            , ("rotate", Bool False)
            ]
    , testCase "report with rotate returns the next lease" $ do
        c <- client url
        l <- expectRight =<< lease c "agi-labs" defaultLeaseOptions
        n <- expectRight =<< report c l (outcomeReport OutcomeQuotaExhausted) {reportRotate = True}
        fmap leaseId n @?= Just "lease-1-next"
        fmap leaseKeyId n @?= Just 21
    , testCase "report twice is ReportFailed" $ do
        c <- client url
        l <- expectRight =<< lease c "agi-labs" defaultLeaseOptions
        e <- expectLeft =<< report c l {leaseId = "done"} (outcomeReport OutcomeOk)
        assertBool (show e) (case e of ReportFailed _ -> True; _ -> False)
    , testCase "outcome names" $
        map outcomeText [minBound .. maxBound] @?= ["ok", "rate_limited", "busy", "quota_exhausted", "invalid", "banned", "error"]
    , testCase "withKey rotates on a key problem and reports ok" $ do
        c <- client url
        calls <- newIORef (0 :: Int)
        v <- expectRight =<< withKey c "agi-labs" defaultWithKeyOptions (\l -> do
          n <- atomicModifyIORef' calls (\x -> (x + 1, x + 1))
          pure $ if n == 1 then Left (KeyRateLimited (Just 3) (Just 429) Nothing) else Right (leaseId l))
        v @?= "lease-1-next"
        seen <- take 3 <$> readIORef (fakeSeen fake)
        map seenPath seen
          @?= [ ["api", "v1", "leases", "lease-1-next", "report"]
              , ["api", "v1", "leases", "lease-1", "report"]
              , ["api", "v1", "lease"]
              ]
        map (KM.lookup "outcome" . seenJson) (take 1 seen) @?= [Just (String "ok")]
    , testCase "withKey gives up after max attempts" $ do
        c <- client url
        e <- expectLeft =<< withKey c "agi-labs" defaultWithKeyOptions {withKeyMaxAttempts = 2} (\_ -> pure (Left (KeyInvalid (Just 401) Nothing) :: Either KeyProblem ()))
        e @?= RotationExhausted (KeyInvalid (Just 401) Nothing)
    ]

applyTests :: TestTree
applyTests =
  testGroup
    "apply"
    [ testCase "header auth sets the named header" $ do
        let l = mk AuthHeader "X-Api-Key" ""
        applyHeaders l [("Accept", "text/plain"), ("x-api-key", "old")] @?= [("Accept", "text/plain"), ("X-Api-Key", enc)]
        applyQuery l [("q", "1")] @?= [("q", "1")]
        leaseUrl l "/v1/chat" [("q", "1")] @?= "https://agi-labs.example/v1/chat?q=1"
    , testCase "header auth with a prefix" $
        applyHeaders (mk AuthHeader "X-Api-Key" "Token ") [] @?= [("X-Api-Key", "Token " <> enc)]
    , testCase "bearer auth defaults to Authorization" $ do
        applyHeaders (mk AuthBearer "" "") [] @?= [("Authorization", "Bearer " <> enc)]
        applyHeaders (mk AuthBearer "X-Auth" "Key ") [] @?= [("X-Auth", "Key " <> enc)]
    , testCase "query auth sets the named parameter" $ do
        let l = mk AuthQuery "key" ""
        applyQuery l [("q", "1"), ("key", "old")] @?= [("q", "1"), ("key", secret)]
        applyHeaders l [("Accept", "x")] @?= [("Accept", "x")]
        leaseUrl l "v1/chat" [("q", "a b")]
          @?= "https://agi-labs.example/v1/chat?q=a%20b&key=" <> TE.decodeUtf8 (urlEncode True (TE.encodeUtf8 secret))
        applyQuery (mk AuthQuery "" "") [] @?= [("api_key", secret)]
    , testCase "applyToRequest puts the key where it belongs" $ do
        base <- H.parseRequest "https://agi-labs.example/v1/chat?q=1"
        let rq = applyToRequest (mk AuthQuery "key" "") base
        parseQuery (H.queryString rq) @?= [("q", Just "1"), ("key", Just enc)]
        let rh = applyToRequest (mk AuthHeader "X-Api-Key" "") base
        lookup "X-Api-Key" (H.requestHeaders rh) @?= Just enc
        H.queryString rh @?= "?q=1"
    ]
  where
    enc = TE.encodeUtf8 secret
    mk t n p = Lease "l" 1 "agi-labs" secret (Auth t n p) "https://agi-labs.example" Nothing False

meTests :: Fake -> Text -> TestTree
meTests fake url =
  testGroup
    "me"
    [ testCase "me decodes the token's access" $ do
        c <- client url
        m <- expectRight =<< me c
        meApp m @?= MeApp 7 "Website"
        meLimits m @?= Limits (Just 500) Nothing Nothing (Just 20)
        usageTodayRequests (meUsage m) @?= 3
        usageMonthCost (meUsage m) @?= 1.25
        usageLastUsedAt (meUsage m) @?= Just (UTCTime (fromGregorian 2026 11 4) 36000)
        meLimitHit m @?= Nothing
        meServices m @?= [MeService "agi-labs" "AGI Labs" "custom" "ai" 3 (Just "data:image/png;base64,AAAA")]
        meShare m @?= Nothing
        s <- lastSeen fake
        seenMethod s @?= "GET"
        seenPath s @?= ["api", "v1", "me"]
        seenHeader s "Accept" @?= Just "application/json"
    , testCase "services decodes the list" $ do
        c <- client url
        ss <- expectRight =<< services c
        map serviceSlug ss @?= ["agi-labs"]
        map serviceAvailable ss @?= [True]
        map (authType . serviceAuth) ss @?= [AuthBearer]
        map serviceGuideUrl ss @?= ["http://acn.test/api/v1/services/agi-labs/guide"]
    ]

envTests :: Fake -> Text -> TestTree
envTests fake url =
  testGroup
    "environment"
    [ testCase "URL and token come from the environment" $ withEnv [("ACNOLOGIA_URL", Just url), ("ACNOLOGIA_TOKEN", Just "acn_from_env"), ("ACNOLOGIA_ACCESS_PASSWORD", Nothing)] $ do
        c <- newClientOrThrow defaultConfig
        clientBaseUrl c @?= url
        _ <- expectRight =<< me c
        s <- lastSeen fake
        seenHeader s "Authorization" @?= Just "Bearer acn_from_env"
        seenHeader s "X-Acnologia-Access" @?= Nothing
    , testCase "access password from the environment is sent" $ withEnv [("ACNOLOGIA_URL", Just url), ("ACNOLOGIA_TOKEN", Just "acn_from_env"), ("ACNOLOGIA_ACCESS_PASSWORD", Just access)] $ do
        c <- newClientOrThrow defaultConfig
        _ <- expectRight =<< proxyGet c "agi-labs" "v1/chat" []
        s <- lastSeen fake
        seenHeader s "X-Acnologia-Access" @?= Just (TE.encodeUtf8 access)
    , testCase "explicit settings win over the environment" $ withEnv [("ACNOLOGIA_URL", Just "http://127.0.0.1:1"), ("ACNOLOGIA_TOKEN", Just "acn_from_env"), ("ACNOLOGIA_ACCESS_PASSWORD", Nothing)] $ do
        c <- newClientOrThrow defaultConfig {configBaseUrl = Just (url <> "/"), configToken = Just token, configAccessPassword = Just "explicit-pass"}
        clientBaseUrl c @?= url
        _ <- expectRight =<< me c
        s <- lastSeen fake
        seenHeader s "Authorization" @?= Just ("Bearer " <> TE.encodeUtf8 token)
        seenHeader s "X-Acnologia-Access" @?= Just "explicit-pass"
    , testCase "missing URL or token is InvalidInput" $ withEnv [("ACNOLOGIA_URL", Nothing), ("ACNOLOGIA_TOKEN", Nothing), ("ACNOLOGIA_ACCESS_PASSWORD", Nothing)] $ do
        e1 <- expectLeft =<< newClient defaultConfig {configToken = Just token}
        assertBool (show e1) (case e1 of InvalidInput t -> "ACNOLOGIA_URL" `T.isInfixOf` t; _ -> False)
        e2 <- expectLeft =<< newClient defaultConfig {configBaseUrl = Just url}
        assertBool (show e2) (case e2 of InvalidInput t -> "ACNOLOGIA_TOKEN" `T.isInfixOf` t; _ -> False)
    ]

withEnv :: [(String, Maybe Text)] -> IO a -> IO a
withEnv vars act = do
  mapM_ (\(k, v) -> maybe (unsetEnv k) (setEnv k . T.unpack) v) vars
  r <- act
  mapM_ (unsetEnv . fst) vars
  pure r

scrubTests :: Fake -> Text -> TestTree
scrubTests fake url =
  testGroup
    "scrubbing"
    [ testCase "error messages drop the token and the access password" $ do
        c <- newClientOrThrow defaultConfig {configBaseUrl = Just url, configToken = Just token, configAccessPassword = Just access}
        e <- expectLeft =<< proxyGet c "err" "403/service_not_allowed" []
        let shown = T.pack (show e <> displayException e)
        assertBool (T.unpack shown) (not (token `T.isInfixOf` shown))
        assertBool (T.unpack shown) (not (access `T.isInfixOf` shown))
        assertBool (T.unpack shown) ("[redacted]" `T.isInfixOf` shown)
    , testCase "show hides the token, the access password and the secret" $ do
        c <- client url
        assertBool (show c) (not (token `T.isInfixOf` T.pack (show c)))
        let cfg = defaultConfig {configToken = Just token, configAccessPassword = Just access}
        assertBool (show cfg) (not (token `T.isInfixOf` T.pack (show cfg)) && not (access `T.isInfixOf` T.pack (show cfg)))
        l <- expectRight =<< lease c "agi-labs" defaultLeaseOptions
        assertBool (show l) (not (secret `T.isInfixOf` T.pack (show l)))
    , testCase "report messages drop the secret, raw and URL encoded" $ do
        c <- client url
        l <- expectRight =<< lease c "agi-labs" defaultLeaseOptions
        let encoded = TE.decodeUtf8 (urlEncode True (TE.encodeUtf8 secret))
        _ <- expectRight =<< report c l (outcomeReport OutcomeError) {reportMessage = Just ("GET /x?key=" <> encoded <> " with " <> secret <> T.replicate 600 "x")}
        s <- lastSeen fake
        case KM.lookup "message" (seenJson s) of
          Just (String m) -> do
            assertBool (T.unpack m) (not (secret `T.isInfixOf` m) && not (encoded `T.isInfixOf` m))
            T.take 27 m @?= "GET /x?key=[key] with [key]"
            T.length m @?= 500
          other -> assertFailure (show other)
    , testCase "scrubMessage" $ do
        scrubMessage "a sk-1 b" "sk-1" @?= "a [key] b"
        scrubMessage "nothing" "" @?= "nothing"
    , testCase "withKey reports a thrown exception without the secret" $ do
        c <- client url
        r <- try (withKey c "agi-labs" defaultWithKeyOptions (\l -> ioError (userError ("boom " <> T.unpack (leaseSecret l)))) :: IO (Either AcnologiaError ()))
        case r of
          Left (_ :: IOError) -> pure ()
          Right v -> assertFailure (show v)
        s <- lastSeen fake
        KM.lookup "outcome" (seenJson s) @?= Just (String "error")
        case KM.lookup "message" (seenJson s) of
          Just (String m) -> assertBool (T.unpack m) ("[key]" `T.isInfixOf` m && not (secret `T.isInfixOf` m))
          other -> assertFailure (show other)
    ]

-- ---------------------------------------------------------------------------
-- 0.2.0: guides, capabilities, the forward proxy, no-cache, share status

v2Tests :: Fake -> Text -> TestTree
v2Tests fake url =
  testGroup
    "0.2.0"
    [ testCase "guide decodes the JSON guide" $ do
        c <- client url
        g <- expectRight =<< guide c "agi-labs"
        guideText g @?= "# AGI Labs\n"
        guideGenerated g @?= True
        guideStructured g @?= object ["endpoints" .= [object ["path" .= ("/v1/chat" :: Text)]]]
        s <- lastSeen fake
        seenMethod s @?= "GET"
        seenPath s @?= ["api", "v1", "services", "agi-labs", "guide"]
        seenHeader s "Accept" @?= Just "application/json"
        seenHeader s "Authorization" @?= Just ("Bearer " <> TE.encodeUtf8 token)
    , testCase "guideMarkdown asks for Markdown" $ do
        c <- client url
        md <- expectRight =<< guideMarkdown c "agi-labs"
        md @?= "# AGI Labs\n\nCall /v1/chat.\n"
        s <- lastSeen fake
        seenHeader s "Accept" @?= Just "text/markdown"
        seenHeader s "Authorization" @?= Just ("Bearer " <> TE.encodeUtf8 token)
    , testCase "a guide the token may not read is ServiceNotAllowed" $ do
        c <- client url
        e1 <- expectLeft =<< guide c "secret"
        assertBool (show e1) (case e1 of ServiceNotAllowed _ -> True; _ -> False)
        e2 <- expectLeft =<< guideMarkdown c "secret"
        assertBool (show e2) (case e2 of ServiceNotAllowed _ -> True; _ -> False)
    , testCase "capabilities decodes the list" $ do
        c <- client url
        caps <- expectRight =<< capabilities c
        s <- lastSeen fake
        seenMethod s @?= "GET"
        seenPath s @?= ["api", "v1", "capabilities"]
        case caps of
          [k] -> do
            capabilitySlug k @?= "maps-search"
            capabilityName k @?= "Maps search"
            capabilityDescription k @?= "Find places"
            capabilityStrategy k @?= "priority"
            capabilityParams k @?= object ["query" .= object ["type" .= ("string" :: Text), "required" .= True]]
            capabilityRoutes k @?= [CapabilityRoute "omkar-cloud" True, CapabilityRoute "serpapi" False]
            capabilityUrl k @?= "http://acn.test/proxy/maps-search"
          other -> assertFailure (show other)
    , testCase "capability posts the params and reads the answer headers" $ do
        c <- client url
        let opts = defaultCapabilityOptions {capService = Just "serpapi", capRaw = True, capIdempotencyKey = Just "cap-1", capNoCache = True}
        r <- expectRight =<< capability c "maps-search" (object ["query" .= ("cafes" :: Text)]) opts
        s <- lastSeen fake
        seenMethod s @?= "POST"
        seenPath s @?= ["proxy", "maps-search"]
        parseQuery (seenQuery s) @?= [("_service", Just "serpapi"), ("_raw", Just "1")]
        seenJson s @?= KM.fromList [("query", String "cafes")]
        seenHeader s "Content-Type" @?= Just "application/json"
        seenHeader s "Idempotency-Key" @?= Just "cap-1"
        seenHeader s "Cache-Control" @?= Just "no-cache"
        seenHeader s "Authorization" @?= Just ("Bearer " <> TE.encodeUtf8 token)
        let a = responseAcnologia r
        responseStatus r @?= 200
        acnCapability a @?= Just "maps-search"
        acnService a @?= Just "serpapi"
        acnCache a @?= Just "miss"
        acnKeyId a @?= Just 9
        acnAttempts a @?= Just 1
        acnTried a @?= [Tried "omkar-cloud" 503, Tried "serpapi" 200]
    , testCase "capability with default options sends an empty object" $ do
        c <- client url
        r <- expectRight =<< capability c "maps-search" Null defaultCapabilityOptions
        s <- lastSeen fake
        seenQuery s @?= ""
        seenBody s @?= "{}"
        seenHeader s "Cache-Control" @?= Nothing
        seenHeader s "Idempotency-Key" @?= Nothing
        acnCache (responseAcnologia r) @?= Nothing
    , testCase "capability errors from Acnologia are Left" $ do
        c <- client url
        e <- expectLeft =<< capability c "no-route" (object []) defaultCapabilityOptions
        errorCode e @?= "no_route"
        errorStatus e @?= 503
    , testCase "proxyNoCache sends Cache-Control: no-cache" $ do
        c <- client url
        _ <- expectRight =<< proxy c "agi-labs" (proxyRequest "GET" "v1/models") {proxyNoCache = True, proxyHeaders = [("Cache-Control", "max-age=60")]}
        s <- lastSeen fake
        filter ((== "Cache-Control") . fst) (seenHeaders s) @?= [("Cache-Control", "no-cache")]
        _ <- expectRight =<< proxy c "agi-labs" (proxyRequest "GET" "v1/models")
        s2 <- lastSeen fake
        seenHeader s2 "Cache-Control" @?= Nothing
        acnTried . responseAcnologia <$> (expectRight =<< proxy c "agi-labs" (proxyRequest "GET" "v1/models")) >>= (@?= [])
    , testCase "parseTried" $ do
        parseTried "a=200,b=503 , c" @?= [Tried "a" 200, Tried "b" 503, Tried "c" 0]
        parseTried "" @?= []
    , testCase "forwardProxy decodes the setup" $ do
        c <- client url
        fp <- expectRight =<< forwardProxy c
        s <- lastSeen fake
        seenPath s @?= ["api", "v1", "proxy"]
        seenHeader s "Authorization" @?= Just ("Bearer " <> TE.encodeUtf8 token)
        fpProxyUrl fp @?= "http://acn.test:8888"
        fpProxyUrlWithToken fp @?= "http://app:%3Capp%20token%3E@acn.test:8888"
        fpCaUrl fp @?= "http://acn.test/api/v1/proxy/ca.pem"
        fpCaFingerprint fp @?= "AB:CD"
        fpPassthrough fp @?= True
        fpHosts fp @?= [ForwardHost "serpapi.com" "serpapi" "SerpAPI" ""]
        Map.lookup "NODE_USE_ENV_PROXY" (fpEnv fp) @?= Just "1"
        fpServiceHeader fp @?= "X-Acnologia-Service"
        assertBool "show hides the token" (not (token `T.isInfixOf` T.pack (show fp)))
    , testCase "forwardProxyCA sends no token and saveForwardProxyCA writes it" $ do
        c <- client url
        pem <- expectRight =<< forwardProxyCA c
        pem @?= pemText
        s <- lastSeen fake
        seenPath s @?= ["api", "v1", "proxy", "ca.pem"]
        seenHeader s "Authorization" @?= Nothing
        -- Under dist-newstyle, which git ignores: no directory package needed.
        let path = "dist-newstyle/acn-hs-test-ca.pem"
        expectRight =<< saveForwardProxyCA c path
        saved <- BS.readFile path
        saved @?= TE.encodeUtf8 pemText
    , testCase "forwardProxyURL and forwardProxyEnv put the token in" $ do
        c <- newClientOrThrow defaultConfig {configBaseUrl = Just url, configToken = Just "acn_a+b/c="}
        u <- expectRight =<< forwardProxyURL c
        u @?= "http://app:acn_a%2Bb%2Fc%3D@acn.test:8888"
        env <- expectRight =<< forwardProxyEnv c "acnologia-ca.pem"
        env
          @?= Map.fromList
            [ ("HTTPS_PROXY", u)
            , ("HTTP_PROXY", u)
            , ("NODE_USE_ENV_PROXY", "1")
            , ("NODE_EXTRA_CA_CERTS", "acnologia-ca.pem")
            , ("REQUESTS_CA_BUNDLE", "acnologia-ca.pem")
            , ("SSL_CERT_FILE", "acnologia-ca.pem")
            ]
        env2 <- expectRight =<< forwardProxyEnv c "/etc/acn.pem"
        Map.lookup "SSL_CERT_FILE" env2 @?= Just "/etc/acn.pem"
    , testCase "shareUnavailable names the share's status" $ do
        c <- client url
        e <- expectLeft =<< proxyGet c "err" "403/share_paused" []
        shareUnavailable e @?= Just "paused"
        e2 <- expectLeft =<< proxyGet c "err" "429/app_limit_reached" []
        shareUnavailable e2 @?= Nothing
    ]
