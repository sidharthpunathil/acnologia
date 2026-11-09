{-# LANGUAGE LambdaCase #-}
{-# LANGUAGE OverloadedStrings #-}
{-# LANGUAGE ScopedTypeVariables #-}

-- |
-- Module      : Acnologia
-- Description : Client for the Acnologia consumer API
-- License     : MIT
--
-- Acnologia is a self-hosted API key server. An app holds an app token
-- (@acn_...@) instead of provider keys and either sends its requests through
-- the server's proxy, which attaches a key, or leases a key for a short time.
--
-- Every call runs in 'IO' and returns @'Either' 'AcnologiaError' a@. Each one
-- has a variant ending in @OrThrow@ that throws the 'AcnologiaError' instead.
--
-- > import Acnologia
-- >
-- > main :: IO ()
-- > main = do
-- >   client <- newClientOrThrow defaultConfig   -- reads ACNOLOGIA_URL and ACNOLOGIA_TOKEN
-- >   res <- proxyOrThrow client "agi-labs" (proxyRequest "GET" "/v1/models")
-- >   print (responseStatus res)
module Acnologia
  ( -- * Client
    Config (..)
  , defaultConfig
  , Client
  , clientBaseUrl
  , newClient
  , newClientOrThrow
  , acnologiaVersion

    -- * Proxy
  , ProxyRequest (..)
  , proxyRequest
  , withJsonBody
  , Response (..)
  , AcnologiaHeaders (..)
  , proxy
  , proxyOrThrow
  , proxyGet
  , proxyPostJson
  , gatewayError
  , Tried (..)
  , parseTried

    -- * Leases
  , Lease (..)
  , Auth (..)
  , AuthType (..)
  , LeaseOptions (..)
  , defaultLeaseOptions
  , lease
  , leaseOrThrow
  , renew
  , renewOrThrow
  , Outcome (..)
  , outcomeText
  , Report (..)
  , outcomeReport
  , report
  , reportOrThrow
  , applyHeaders
  , applyQuery
  , applyToRequest
  , leaseUrl
  , scrubMessage

    -- * The lease helper
  , KeyProblem (..)
  , WithKeyOptions (..)
  , defaultWithKeyOptions
  , withKey
  , withKeyOrThrow

    -- * Me and services
  , Me (..)
  , MeApp (..)
  , Limits (..)
  , Usage (..)
  , LimitHit (..)
  , MeService (..)
  , Share (..)
  , me
  , meOrThrow
  , Service (..)
  , services
  , servicesOrThrow

    -- * Usage guides
  , Guide (..)
  , guide
  , guideOrThrow
  , guideMarkdown
  , guideMarkdownOrThrow

    -- * Capabilities
  , Capability (..)
  , CapabilityRoute (..)
  , capabilities
  , capabilitiesOrThrow
  , CapabilityOptions (..)
  , defaultCapabilityOptions
  , capability
  , capabilityOrThrow

    -- * Forward proxy
  , ForwardProxy (..)
  , ForwardHost (..)
  , forwardProxy
  , forwardProxyOrThrow
  , forwardProxyCA
  , forwardProxyCAOrThrow
  , saveForwardProxyCA
  , saveForwardProxyCAOrThrow
  , forwardProxyURL
  , forwardProxyURLOrThrow
  , forwardProxyEnv
  , forwardProxyEnvOrThrow

    -- * Errors
  , AcnologiaError (..)
  , ErrorInfo (..)
  , errorInfo
  , errorStatus
  , errorCode
  , errorRetryAfter
  , shareUnavailable
  , orThrow
  ) where

import Control.Exception (Exception (..), IOException, SomeException, catch, throwIO, try)
import Data.Aeson (FromJSON (..), ToJSON (..), Value (..), object, withObject, (.!=), (.:), (.:?), (.=))
import qualified Data.Aeson as A
import qualified Data.Aeson.Key as AK
import qualified Data.Aeson.KeyMap as KM
import qualified Data.Aeson.Types as AT
import qualified Data.ByteString as BS
import qualified Data.ByteString.Char8 as BC
import qualified Data.ByteString.Lazy as LBS
import qualified Data.CaseInsensitive as CI
import Data.Char (isSpace)
import qualified Data.Map.Strict as Map
import Data.Maybe (catMaybes, fromMaybe, isJust)
import Data.Text (Text)
import qualified Data.Text as T
import qualified Data.Text.Encoding as TE
import qualified Data.Text.Encoding.Error as TEE
import Data.Time (UTCTime, defaultTimeLocale, diffUTCTime, getCurrentTime, parseTimeM)
import qualified Network.HTTP.Client as H
import qualified Network.HTTP.Client.TLS as TLS
import Network.HTTP.Types (Method, Query, RequestHeaders, ResponseHeaders, HeaderName)
import qualified Network.HTTP.Types as HT
import System.Environment (lookupEnv)

-- ---------------------------------------------------------------------------
-- Client

-- | The SDK version, sent in the @User-Agent@ and @X-Acnologia-Client@ headers.
acnologiaVersion :: Text
acnologiaVersion = "0.2.0"

userAgent :: BS.ByteString
userAgent = "acnologia-haskell/" <> TE.encodeUtf8 acnologiaVersion

-- | How to build a 'Client'. Start from 'defaultConfig' and set what you need.
--
-- Fields left as 'Nothing' come from the environment when 'newClient' runs:
-- 'configBaseUrl' from @ACNOLOGIA_URL@, 'configToken' from @ACNOLOGIA_TOKEN@ and
-- 'configAccessPassword' from @ACNOLOGIA_ACCESS_PASSWORD@.
data Config = Config
  { configBaseUrl :: Maybe Text
    -- ^ Where Acnologia runs, for example @http://127.0.0.1:8080@.
  , configToken :: Maybe Text
    -- ^ The app token, @acn_...@.
  , configAccessPassword :: Maybe Text
    -- ^ Sent as @X-Acnologia-Access@ when the server sits behind an access
    -- password. Only sent when set here or in @ACNOLOGIA_ACCESS_PASSWORD@.
  , configTimeoutSec :: Int
    -- ^ Timeout for API calls (lease, report, renew, me, services) in
    -- seconds. Default 75, longer than the 60 second maximum lease wait. 0
    -- disables it.
  , configProxyTimeoutSec :: Int
    -- ^ Timeout for proxy calls in seconds. Default 300. 0 disables it.
  , configManager :: Maybe H.Manager
    -- ^ An http-client manager to share. A TLS manager is made when absent.
  }

-- | Shows the settings with the token and the access password hidden.
instance Show Config where
  show c =
    "Config {configBaseUrl = " <> show (configBaseUrl c)
      <> ", configToken = " <> hidden (configToken c)
      <> ", configAccessPassword = " <> hidden (configAccessPassword c)
      <> ", configTimeoutSec = " <> show (configTimeoutSec c)
      <> ", configProxyTimeoutSec = " <> show (configProxyTimeoutSec c)
      <> "}"
    where
      hidden = maybe "Nothing" (const "Just <redacted>")

-- | Everything from the environment, a 75 second API timeout and a 300 second
-- proxy timeout.
defaultConfig :: Config
defaultConfig =
  Config
    { configBaseUrl = Nothing
    , configToken = Nothing
    , configAccessPassword = Nothing
    , configTimeoutSec = 75
    , configProxyTimeoutSec = 300
    , configManager = Nothing
    }

-- | A configured client. Build one with 'newClient'; it is safe to share
-- between threads.
data Client = Client
  { clientBaseUrl :: !Text
    -- ^ The server URL without a trailing slash.
  , clientToken :: !Text
  , clientAccess :: !(Maybe Text)
  , clientTimeout :: !Int
  , clientProxyTimeout :: !Int
  , clientManager :: !H.Manager
  }

-- | Shows the base URL only. The token never appears.
instance Show Client where
  show c = "Client {clientBaseUrl = " <> show (clientBaseUrl c) <> ", token = <redacted>}"

-- | Build a client, filling unset fields from the environment.
-- Fails with 'InvalidInput' when the URL or the token is missing.
newClient :: Config -> IO (Either AcnologiaError Client)
newClient cfg = do
  envUrl <- lookupNonEmpty "ACNOLOGIA_URL"
  envTok <- lookupNonEmpty "ACNOLOGIA_TOKEN"
  envAccess <- lookupNonEmpty "ACNOLOGIA_ACCESS_PASSWORD"
  let url = nonEmpty (configBaseUrl cfg) `orElse` envUrl
      tok = nonEmpty (configToken cfg) `orElse` envTok
      access = nonEmpty (configAccessPassword cfg) `orElse` envAccess
  case (url, tok) of
    (Nothing, _) -> pure (Left (InvalidInput "base URL is required (set configBaseUrl or ACNOLOGIA_URL)"))
    (_, Nothing) -> pure (Left (InvalidInput "token is required (set configToken or ACNOLOGIA_TOKEN)"))
    (Just u, Just t)
      | not ("http://" `T.isPrefixOf` u || "https://" `T.isPrefixOf` u) ->
          pure (Left (InvalidInput "base URL must start with http:// or https://"))
      | otherwise -> do
          mgr <- maybe (H.newManager TLS.tlsManagerSettings) pure (configManager cfg)
          pure . Right $
            Client
              { clientBaseUrl = T.dropWhileEnd (== '/') u
              , clientToken = t
              , clientAccess = access
              , clientTimeout = max 0 (configTimeoutSec cfg)
              , clientProxyTimeout = max 0 (configProxyTimeoutSec cfg)
              , clientManager = mgr
              }
  where
    lookupNonEmpty k = nonEmpty . fmap T.pack <$> lookupEnv k
    nonEmpty = \case
      Just v | not (T.null (T.strip v)) -> Just v
      _ -> Nothing
    orElse (Just a) _ = Just a
    orElse Nothing b = b

-- | 'newClient' that throws.
newClientOrThrow :: Config -> IO Client
newClientOrThrow = orThrow . newClient

-- ---------------------------------------------------------------------------
-- Errors

-- | What the server said about a failed call.
data ErrorInfo = ErrorInfo
  { infoStatus :: !Int
    -- ^ HTTP status.
  , infoCode :: !Text
    -- ^ The @error.code@ from the body, empty when there was none.
  , infoMessage :: !Text
    -- ^ The server's message, with the token and the access password removed.
  }
  deriving (Eq, Show)

-- | Everything a call can fail with. Server errors are mapped from the HTTP
-- status and the @error.code@ in the body. Messages never carry the app
-- token, the access password or a lease secret.
data AcnologiaError
  = -- | 401, or 403 @unauthenticated@: the token is missing, wrong or its app is disabled.
    Unauthenticated !ErrorInfo
  | -- | 403 @service_not_allowed@: this token may not use that service.
    ServiceNotAllowed !ErrorInfo
  | -- | 403 @proxy_not_allowed@: the app is lease only.
    ProxyNotAllowed !ErrorInfo
  | -- | 403 @lease_not_allowed@: the app is proxy only, or the service is shared
    -- from another server and so proxy only.
    LeaseNotAllowed !ErrorInfo
  | -- | 503 @no_key_available@: every usable key is cooling down, exhausted or
    -- excluded. Carries the seconds to wait, when known.
    NoKeyAvailable !ErrorInfo !(Maybe Double)
  | -- | 503 @all_keys_busy@: every usable key is at its concurrency limit.
    -- Retry after the given seconds or lease with a wait.
    AllKeysBusy !ErrorInfo !(Maybe Double)
  | -- | 503 @service_unavailable@: the provider keeps failing and its circuit
    -- breaker is open.
    ServiceUnavailable !ErrorInfo !(Maybe Double)
  | -- | 429 @app_limit_reached@: the app used up a request or budget limit.
    -- Carries the limit (for example @daily_requests@), when it resets and the
    -- seconds to wait.
    AppLimitReached !ErrorInfo !Text !(Maybe UTCTime) !(Maybe Double)
  | -- | 403 @share_revoked@: the owner revoked the share this token belongs to.
    ShareRevoked !ErrorInfo
  | -- | 403 @share_paused@: the owner paused the share.
    SharePaused !ErrorInfo
  | -- | 403 @share_expired@: the share ran out.
    ShareExpired !ErrorInfo
  | -- | 404 @lease_not_found@: renewing a lease that is no longer open.
    LeaseNotFound !ErrorInfo
  | -- | 409 or 410 @report_failed@: the lease was already reported or expired
    -- long ago.
    ReportFailed !ErrorInfo
  | -- | 502 @upstream_unreachable@: the provider could not be reached.
    UpstreamUnreachable !ErrorInfo
  | -- | 502 @upstream_blocked@: the provider address is link local or a
    -- metadata address, and Acnologia refused to connect.
    UpstreamBlocked !ErrorInfo
  | -- | 508 @loop_detected@: the request passed through too many Acnologia servers.
    LoopDetected !ErrorInfo
  | -- | 400 @bad_path@: the proxy path has a literal @?@, @#@ or a dot segment.
    BadPath !ErrorInfo
  | -- | 413 @body_too_large@: the proxy body is over the server's limit.
    BodyTooLarge !ErrorInfo
  | -- | Any other error answer from Acnologia.
    ApiError !ErrorInfo
  | -- | The lease helper ran out of attempts. Carries the last problem.
    RotationExhausted !KeyProblem
  | -- | The request could not be sent or the answer not read.
    HttpFailure !Text
  | -- | Acnologia answered 2xx with a body this SDK could not decode.
    BadResponse !Text
  | -- | A missing setting or an argument out of range.
    InvalidInput !Text
  deriving (Eq, Show)

instance Exception AcnologiaError where
  displayException e = T.unpack ("acnologia: " <> describe e)
    where
      describe err = case errorInfo err of
        Just i ->
          T.intercalate ", " . filter (not . T.null) $
            [ "http " <> tshow (infoStatus i)
            , infoCode i
            , infoMessage i
            , maybe "" (\s -> "retry after " <> tshow s <> "s") (errorRetryAfter err)
            ]
        Nothing -> case err of
          RotationExhausted p -> "no key worked: " <> keyProblemText p
          HttpFailure t -> "request failed: " <> t
          BadResponse t -> "bad response: " <> t
          InvalidInput t -> t
          _ -> T.pack (show err)

-- | The server's details, for errors that came from the server.
errorInfo :: AcnologiaError -> Maybe ErrorInfo
errorInfo = \case
  Unauthenticated i -> Just i
  ServiceNotAllowed i -> Just i
  ProxyNotAllowed i -> Just i
  LeaseNotAllowed i -> Just i
  NoKeyAvailable i _ -> Just i
  AllKeysBusy i _ -> Just i
  ServiceUnavailable i _ -> Just i
  AppLimitReached i _ _ _ -> Just i
  ShareRevoked i -> Just i
  SharePaused i -> Just i
  ShareExpired i -> Just i
  LeaseNotFound i -> Just i
  ReportFailed i -> Just i
  UpstreamUnreachable i -> Just i
  UpstreamBlocked i -> Just i
  LoopDetected i -> Just i
  BadPath i -> Just i
  BodyTooLarge i -> Just i
  ApiError i -> Just i
  RotationExhausted _ -> Nothing
  HttpFailure _ -> Nothing
  BadResponse _ -> Nothing
  InvalidInput _ -> Nothing

-- | The HTTP status, or 0 for errors that did not come from the server.
errorStatus :: AcnologiaError -> Int
errorStatus = maybe 0 infoStatus . errorInfo

-- | The server's error code, or empty for errors that did not come from the server.
errorCode :: AcnologiaError -> Text
errorCode = maybe "" infoCode . errorInfo

-- | Seconds to wait before retrying, for the errors that carry it. Taken from
-- @retry_after_sec@ in the body, else from the @Retry-After@ header.
errorRetryAfter :: AcnologiaError -> Maybe Double
errorRetryAfter = \case
  NoKeyAvailable _ r -> r
  AllKeysBusy _ r -> r
  ServiceUnavailable _ r -> r
  AppLimitReached _ _ _ r -> r
  _ -> Nothing

-- | For the three ways a share stops working ('ShareRevoked', 'SharePaused'
-- and 'ShareExpired', together the share unavailable errors): the share's
-- status, @revoked@, @paused@ or @expired@, as also sent in
-- @X-Acnologia-Share@. 'Nothing' for any other error.
shareUnavailable :: AcnologiaError -> Maybe Text
shareUnavailable = \case
  ShareRevoked _ -> Just "revoked"
  SharePaused _ -> Just "paused"
  ShareExpired _ -> Just "expired"
  _ -> Nothing

-- | Turn a @Left@ into a thrown 'AcnologiaError'.
orThrow :: IO (Either AcnologiaError a) -> IO a
orThrow act = act >>= either throwIO pure

-- | Map an error answer from Acnologia to an 'AcnologiaError'.
decodeError :: UTCTime -> [Text] -> HT.Status -> ResponseHeaders -> LBS.ByteString -> AcnologiaError
decodeError now secrets st hdrs body =
  case code of
    _ | status == 401 -> Unauthenticated info
    "unauthenticated" -> Unauthenticated info
    "service_not_allowed" -> ServiceNotAllowed info
    "proxy_not_allowed" -> ProxyNotAllowed info
    "lease_not_allowed" -> LeaseNotAllowed info
    "no_key_available" -> NoKeyAvailable info retry
    "all_keys_busy" -> AllKeysBusy info retry
    "service_unavailable" -> ServiceUnavailable info retry
    "app_limit_reached" -> AppLimitReached info limitName resetAt retry
    "share_revoked" -> ShareRevoked info
    "share_paused" -> SharePaused info
    "share_expired" -> ShareExpired info
    "lease_not_found" -> LeaseNotFound info
    "report_failed" -> ReportFailed info
    "upstream_unreachable" -> UpstreamUnreachable info
    "upstream_blocked" -> UpstreamBlocked info
    "loop_detected" -> LoopDetected info
    "bad_path" -> BadPath info
    "body_too_large" -> BodyTooLarge info
    "" | status == 503 -> NoKeyAvailable info {infoCode = "no_key_available"} retry
    _ -> ApiError info
  where
    obj = case A.decode body of
      Just (Object o) -> Just o
      _ -> Nothing
    field k = obj >>= KM.lookup (AK.fromText k)
    errObj = field "error"
    (bodyCode, bodyMsg) = case errObj of
      Just (Object e) -> (textAt "code" e, textAt "message" e)
      Just (String s) -> (Nothing, Just s)
      _ -> (Nothing, Nothing)
    textAt k o = case KM.lookup (AK.fromText k) o of
      Just (String s) | not (T.null s) -> Just s
      _ -> Nothing
    shareHeader = case lookup "X-Acnologia-Share" hdrs of
      Just v -> Just ("share_" <> T.toLower (decodeLenient v))
      Nothing -> Nothing
    code = case bodyCode of
      Just c -> c
      Nothing | status == 403 -> fromMaybe "" shareHeader
      Nothing -> ""
    status = HT.statusCode st
    reason = case HT.statusMessage st of
      "" -> "HTTP " <> tshow status
      m -> decodeLenient m
    message = scrubAll secrets (fromMaybe reason bodyMsg)
    info = ErrorInfo {infoStatus = status, infoCode = code, infoMessage = message}
    bodyRetry = case field "retry_after_sec" of
      Just (Number n) | n > 0 -> Just (realToFrac n)
      _ -> Nothing
    retry = case bodyRetry of
      Just r -> Just r
      Nothing -> lookup "Retry-After" hdrs >>= parseRetryAfter now
    limitName = case field "limit" of
      Just (String s) -> s
      _ -> ""
    resetAt = field "reset_at" >>= AT.parseMaybe parseJSON

-- | Parse a @Retry-After@ value: seconds, or an HTTP date.
parseRetryAfter :: UTCTime -> BS.ByteString -> Maybe Double
parseRetryAfter now raw =
  case reads s :: [(Double, String)] of
    [(n, rest)] | all isSpace rest -> if n > 0 then Just n else Nothing
    _ -> do
      t <- parseTimeM True defaultTimeLocale "%a, %d %b %Y %H:%M:%S GMT" s
      let d = realToFrac (diffUTCTime t now) :: Double
      if d > 0 then Just (fromIntegral (ceiling d :: Int)) else Nothing
  where
    s = BC.unpack (BC.strip raw)

-- ---------------------------------------------------------------------------
-- HTTP plumbing

tshow :: Show a => a -> Text
tshow = T.pack . show

decodeLenient :: BS.ByteString -> Text
decodeLenient = TE.decodeUtf8With TEE.lenientDecode

secretsOf :: Client -> [Text]
secretsOf c = clientToken c : maybe [] pure (clientAccess c)

-- | Remove each secret, raw and URL encoded.
scrubAll :: [Text] -> Text -> Text
scrubAll secrets msg = foldl strip1 msg (filter (not . T.null) secrets)
  where
    strip1 m s =
      T.replace (decodeLenient (HT.urlEncode True (TE.encodeUtf8 s))) "[redacted]" (T.replace s "[redacted]" m)

authHeaders :: Client -> RequestHeaders
authHeaders c =
  [ ("Authorization", "Bearer " <> TE.encodeUtf8 (clientToken c))
  , ("User-Agent", userAgent)
  , ("X-Acnologia-Client", userAgent)
  ]
    <> maybe [] (\p -> [("X-Acnologia-Access", TE.encodeUtf8 p)]) (clientAccess c)

-- Header names the SDK owns. A caller's copy of one of these is dropped.
ownedHeaders :: [HeaderName]
ownedHeaders = ["Authorization", "X-Acnologia-Access", "X-Acnologia-Client", "X-Acnologia-Token"]

-- | Send a request and read the whole answer. Never follows redirects, so the
-- token is not replayed to another host.
send :: Client -> Int -> Method -> Text -> RequestHeaders -> Maybe LBS.ByteString -> IO (Either AcnologiaError (H.Response LBS.ByteString))
send c timeoutSec method url hdrs body =
  case H.parseRequest (T.unpack url) of
    Left (_ :: SomeException) -> pure (Left (InvalidInput "could not build the request URL"))
    Right req0 -> do
      let req =
            req0
              { H.method = method
              , H.requestHeaders = hdrs
              , H.requestBody = maybe (H.RequestBodyLBS "") H.RequestBodyLBS body
              , H.redirectCount = 0
              , H.responseTimeout =
                  if timeoutSec > 0 then H.responseTimeoutMicro (timeoutSec * 1000000) else H.responseTimeoutNone
              }
      r <- try (H.httpLbs req (clientManager c))
      pure $ case r of
        Right resp -> Right resp
        Left (e :: H.HttpException) -> Left (HttpFailure (scrubAll (secretsOf c) (describeHttp e)))

describeHttp :: H.HttpException -> Text
describeHttp = \case
  H.InvalidUrlException _ why -> "invalid URL: " <> T.pack why
  H.HttpExceptionRequest _ content -> case content of
    H.ResponseTimeout -> "timed out waiting for the response"
    H.ConnectionTimeout -> "timed out connecting"
    H.ConnectionFailure e -> "could not connect: " <> T.pack (displayException e)
    H.ConnectionClosed -> "connection closed"
    other -> T.pack (takeWhile (/= ' ') (show other))

-- | Call a JSON route under the consumer API and decode the answer.
callJson :: FromJSON a => Client -> Int -> Method -> Text -> Maybe Value -> IO (Either AcnologiaError a)
callJson c timeoutSec method path body = do
  let hdrs =
        authHeaders c
          <> [("Accept", "application/json")]
          <> maybe [] (const [("Content-Type", "application/json")]) body
  r <- send c timeoutSec method (clientBaseUrl c <> path) hdrs (A.encode <$> body)
  case r of
    Left e -> pure (Left e)
    Right resp -> do
      let status = HT.statusCode (H.responseStatus resp)
      if status >= 200 && status < 300
        then pure $ case A.eitherDecode (nullIfEmpty (H.responseBody resp)) of
          Right v -> Right v
          Left why -> Left (BadResponse (T.pack why))
        else do
          now <- getCurrentTime
          pure (Left (decodeError now (secretsOf c) (H.responseStatus resp) (H.responseHeaders resp) (H.responseBody resp)))
  where
    nullIfEmpty b = if LBS.null b then "{}" else b

encodeSegment :: Text -> Text
encodeSegment = decodeLenient . HT.urlEncode False . TE.encodeUtf8

-- ---------------------------------------------------------------------------
-- Proxy

-- | A request to send through @/proxy/{service}/{path}@.
data ProxyRequest = ProxyRequest
  { proxyMethod :: Method
    -- ^ GET, HEAD, POST, PUT, PATCH, DELETE or OPTIONS.
  , proxyPath :: Text
    -- ^ The provider path, for example @/v1/chat@. A leading slash is optional.
  , proxyQuery :: [(Text, Text)]
    -- ^ Query parameters, encoded for you.
  , proxyHeaders :: RequestHeaders
    -- ^ Headers for the provider. @Authorization@ and the @X-Acnologia@
    -- credential headers are set by the client and replace yours.
  , proxyBody :: Maybe LBS.ByteString
    -- ^ The request body, sent as is.
  , proxyIdempotencyKey :: Maybe Text
    -- ^ Sent as @Idempotency-Key@. A retry with the same key within the
    -- server's window replays the first answer instead of calling the provider
    -- again.
  , proxyNoCache :: Bool
    -- ^ Sends @Cache-Control: no-cache@, so the answer never comes from the
    -- proxy's cache.
  }
  deriving (Eq, Show)

-- | A request with no query, headers, body or idempotency key.
proxyRequest :: Method -> Text -> ProxyRequest
proxyRequest m p =
  ProxyRequest
    { proxyMethod = m
    , proxyPath = p
    , proxyQuery = []
    , proxyHeaders = []
    , proxyBody = Nothing
    , proxyIdempotencyKey = Nothing
    , proxyNoCache = False
    }

-- | Set a JSON body, and @Content-Type: application/json@ unless a content
-- type is already set.
withJsonBody :: ToJSON a => a -> ProxyRequest -> ProxyRequest
withJsonBody v r =
  r
    { proxyBody = Just (A.encode v)
    , proxyHeaders =
        if any ((== "Content-Type") . fst) (proxyHeaders r)
          then proxyHeaders r
          else proxyHeaders r <> [("Content-Type", "application/json")]
    }

-- | The @X-Acnologia-*@ headers on a proxied answer.
data AcnologiaHeaders = AcnologiaHeaders
  { acnKeyId :: Maybe Int
    -- ^ @X-Acnologia-Key-Id@: the key that served the request. Absent for a
    -- share's token.
  , acnAttempts :: Maybe Int
    -- ^ @X-Acnologia-Attempts@: how many keys were tried.
  , acnService :: Maybe Text
    -- ^ @X-Acnologia-Service@: the service that answered (useful for groups).
  , acnCache :: Maybe Text
    -- ^ @X-Acnologia-Cache@: @hit@, @miss@, @bypass@ or @replay@.
  , acnReplayed :: Bool
    -- ^ True when @Idempotent-Replayed: true@ was set.
  , acnCapability :: Maybe Text
    -- ^ @X-Acnologia-Capability@: set on a capability call.
  , acnTried :: [Tried]
    -- ^ @X-Acnologia-Tried@: every route a capability call tried, in order.
  , acnAll :: Map.Map Text Text
    -- ^ Every @X-Acnologia-*@ header, keyed by its lower case name.
  }
  deriving (Eq, Show)

-- | A proxied answer, returned as the provider sent it.
data Response = Response
  { responseStatus :: Int
  , responseHeaders :: ResponseHeaders
  , responseBody :: LBS.ByteString
  , responseAcnologia :: AcnologiaHeaders
  }
  deriving (Eq, Show)

-- | One route a capability call tried.
data Tried = Tried
  { triedService :: Text
  , triedStatus :: Int
    -- ^ The route's status, 0 when it failed before an answer.
  }
  deriving (Eq, Show)

-- | Parse an @X-Acnologia-Tried@ value: @service=status@ pairs, comma
-- separated.
parseTried :: Text -> [Tried]
parseTried = map one . filter (not . T.null) . map T.strip . T.splitOn ","
  where
    one part =
      let (name, rest) = T.breakOnEnd "=" part
       in if T.null name
            then Tried part 0
            else Tried (T.strip (T.dropEnd 1 name)) (readInt (T.strip rest))
    readInt t = case reads (T.unpack t) of
      [(n, "")] -> n
      _ -> 0

readAcnologiaHeaders :: ResponseHeaders -> AcnologiaHeaders
readAcnologiaHeaders hdrs =
  AcnologiaHeaders
    { acnKeyId = num "x-acnologia-key-id"
    , acnAttempts = num "x-acnologia-attempts"
    , acnService = Map.lookup "x-acnologia-service" allH
    , acnCache = Map.lookup "x-acnologia-cache" allH
    , acnReplayed = maybe False ((== "true") . T.toLower . decodeLenient) (lookup "Idempotent-Replayed" hdrs)
    , acnCapability = Map.lookup "x-acnologia-capability" allH
    , acnTried = maybe [] parseTried (Map.lookup "x-acnologia-tried" allH)
    , acnAll = allH
    }
  where
    allH =
      Map.fromList
        [ (decodeLenient (CI.foldedCase k), decodeLenient v)
        | (k, v) <- hdrs
        , "x-acnologia-" `BS.isPrefixOf` CI.foldedCase k
        ]
    num k = Map.lookup k allH >>= \v -> case reads (T.unpack v) of
      [(n, "")] -> Just n
      _ -> Nothing

-- | Send a request through Acnologia's proxy. Acnologia attaches a key and
-- moves to the next one on a rate limit; the secret never reaches you.
--
-- The provider's answer comes back as 'Right', whatever its status, so a 404
-- or 429 from the provider is yours to read. 'Left' means Acnologia itself
-- refused or failed (see 'gatewayError'), or the request could not be sent.
proxy :: Client -> Text -> ProxyRequest -> IO (Either AcnologiaError Response)
proxy c service pr
  | T.null service = pure (Left (InvalidInput "service is required"))
  | otherwise =
      sendProxy c (clientBaseUrl c <> "/proxy/" <> encodeSegment service <> "/" <> T.dropWhile (== '/') (proxyPath pr)) pr

-- | Send a proxy request to a URL (without its query), with the SDK's
-- headers and the request's options.
sendProxy :: Client -> Text -> ProxyRequest -> IO (Either AcnologiaError Response)
sendProxy c base pr = do
      let query = HT.renderQuery True (HT.queryTextToQuery [(k, Just v) | (k, v) <- proxyQuery pr])
          url = base <> decodeLenient query
          userHdrs =
            [ h
            | h@(k, _) <- proxyHeaders pr
            , k `notElem` ownedHeaders
            , not (isJust (proxyIdempotencyKey pr) && k == "Idempotency-Key")
            , not (proxyNoCache pr && k == "Cache-Control")
            ]
          idem = maybe [] (\k -> [("Idempotency-Key", TE.encodeUtf8 k)]) (proxyIdempotencyKey pr)
          noCache = [("Cache-Control", "no-cache") | proxyNoCache pr]
          hdrs = userHdrs <> idem <> noCache <> authHeaders c
      r <- send c (clientProxyTimeout c) (proxyMethod pr) url hdrs (proxyBody pr)
      case r of
        Left e -> pure (Left e)
        Right raw -> do
          now <- getCurrentTime
          let resp =
                Response
                  { responseStatus = HT.statusCode (H.responseStatus raw)
                  , responseHeaders = H.responseHeaders raw
                  , responseBody = H.responseBody raw
                  , responseAcnologia = readAcnologiaHeaders (H.responseHeaders raw)
                  }
          pure $ maybe (Right resp) Left (gatewayErrorAt now (secretsOf c) resp)

-- | 'proxy' that throws.
proxyOrThrow :: Client -> Text -> ProxyRequest -> IO Response
proxyOrThrow c s = orThrow . proxy c s

-- | GET a path with query parameters through the proxy.
proxyGet :: Client -> Text -> Text -> [(Text, Text)] -> IO (Either AcnologiaError Response)
proxyGet c s p q = proxy c s (proxyRequest "GET" p) {proxyQuery = q}

-- | POST a JSON body through the proxy.
proxyPostJson :: ToJSON a => Client -> Text -> Text -> a -> IO (Either AcnologiaError Response)
proxyPostJson c s p v = proxy c s (withJsonBody v (proxyRequest "POST" p))

-- | The 'AcnologiaError' in a proxied answer, when Acnologia itself wrote it.
--
-- Answers relayed from the provider carry @X-Acnologia-Service@,
-- @X-Acnologia-Cache@ or @X-Acnologia-Key-Id@; Acnologia's own errors carry
-- none of them and have an @error@ object in the body.
gatewayError :: Response -> IO (Maybe AcnologiaError)
gatewayError r = do
  now <- getCurrentTime
  pure (gatewayErrorAt now [] r)

gatewayErrorAt :: UTCTime -> [Text] -> Response -> Maybe AcnologiaError
gatewayErrorAt now secrets r
  | responseStatus r < 400 = Nothing
  | relayed = Nothing
  | hasErrorObject || responseStatus r == 401 =
      Just (decodeError now secrets (HT.mkStatus (responseStatus r) "") (responseHeaders r) (responseBody r))
  | otherwise = Nothing
  where
    a = responseAcnologia r
    relayed = isJust (acnService a) || isJust (acnCache a) || isJust (acnKeyId a)
    hasErrorObject = case A.decode (responseBody r) of
      Just (Object o) -> isJust (KM.lookup "error" o)
      _ -> False

-- ---------------------------------------------------------------------------
-- Leases

-- | How a service takes its key.
data AuthType
  = -- | A query parameter named by 'authName' (default @api_key@).
    AuthQuery
  | -- | A header named by 'authName' (default @Authorization@).
    AuthHeader
  | -- | @Authorization: Bearer <secret>@, or the header 'authName' names.
    AuthBearer
  | -- | A type this SDK does not know. Treated like 'AuthHeader'.
    AuthOther Text
  deriving (Eq, Show)

-- | Where the secret goes.
data Auth = Auth
  { authType :: AuthType
  , authName :: Text
  , authPrefix :: Text
    -- ^ Put in front of the secret for every type when not empty. For
    -- 'AuthBearer' it replaces the default @Bearer @ prefix.
  }
  deriving (Eq, Show)

instance FromJSON Auth where
  parseJSON = withObject "Auth" $ \o -> do
    t <- o .:? "type" .!= ""
    Auth
      <$> pure (case (t :: Text) of
                  "query" -> AuthQuery
                  "header" -> AuthHeader
                  "bearer" -> AuthBearer
                  other -> AuthOther other)
      <*> o .:? "name" .!= ""
      <*> o .:? "prefix" .!= ""

-- | A key handed out by Acnologia. Report how it went with 'report'.
--
-- 'show' hides the secret.
data Lease = Lease
  { leaseId :: Text
  , leaseKeyId :: Int
  , leaseService :: Text
  , leaseSecret :: Text
    -- ^ The raw key. Keep it out of logs.
  , leaseAuth :: Auth
  , leaseBaseUrl :: Text
    -- ^ The provider's base URL, without a trailing slash.
  , leaseExpiresAt :: Maybe UTCTime
  , leaseExclusive :: Bool
    -- ^ True when the key is reserved for this lease only.
  }
  deriving (Eq)

instance Show Lease where
  show l =
    "Lease {leaseId = " <> show (leaseId l)
      <> ", leaseKeyId = " <> show (leaseKeyId l)
      <> ", leaseService = " <> show (leaseService l)
      <> ", leaseSecret = <redacted>"
      <> ", leaseAuth = " <> show (leaseAuth l)
      <> ", leaseBaseUrl = " <> show (leaseBaseUrl l)
      <> ", leaseExpiresAt = " <> show (leaseExpiresAt l)
      <> ", leaseExclusive = " <> show (leaseExclusive l)
      <> "}"

instance FromJSON Lease where
  parseJSON = withObject "Lease" $ \o ->
    Lease
      <$> o .: "lease_id"
      <*> o .:? "key_id" .!= 0
      <*> o .:? "service" .!= ""
      <*> o .: "secret"
      <*> o .:? "auth" .!= Auth AuthHeader "" ""
      <*> (T.dropWhileEnd (== '/') <$> o .:? "base_url" .!= "")
      <*> o .:? "expires_at"
      <*> o .:? "exclusive" .!= False

-- | Options for 'lease'.
data LeaseOptions = LeaseOptions
  { leaseExcludeKeyIds :: [Int]
    -- ^ Key ids the server must not hand out, for example keys you tried.
  , leaseTtlSec :: Maybe Int
    -- ^ Lease lifetime. The server default applies when 'Nothing'.
  , leaseWaitSec :: Maybe Int
    -- ^ Seconds (0 to 60) the server may wait for a free slot when every
    -- usable key is busy. 'Nothing' or 0 fails fast with 'AllKeysBusy'.
  , leaseExclusiveKey :: Bool
    -- ^ Reserve the key for this lease only until it is reported or expires.
  }
  deriving (Eq, Show)

-- | No exclusions, server default TTL, no wait, shared key.
defaultLeaseOptions :: LeaseOptions
defaultLeaseOptions = LeaseOptions [] Nothing Nothing False

-- | Borrow a key. Always 'report' it: on services with a concurrency limit an
-- open lease holds one of the key's slots until it is reported or expires.
lease :: Client -> Text -> LeaseOptions -> IO (Either AcnologiaError Lease)
lease c service opts
  | T.null service = pure (Left (InvalidInput "service is required"))
  | maybe False (\w -> w < 0 || w > 60) (leaseWaitSec opts) =
      pure (Left (InvalidInput "wait must be between 0 and 60 seconds"))
  | otherwise = callJson c timeout "POST" "/api/v1/lease" (Just body)
  where
    wait = fromMaybe 0 (leaseWaitSec opts)
    timeout
      | clientTimeout c == 0 = 0
      | wait > 0 = max (clientTimeout c) (wait + 10)
      | otherwise = clientTimeout c
    body =
      object . catMaybes $
        [ Just ("service" .= service)
        , if null (leaseExcludeKeyIds opts) then Nothing else Just ("exclude_key_ids" .= leaseExcludeKeyIds opts)
        , ("ttl_sec" .=) <$> leaseTtlSec opts
        , if wait > 0 then Just ("wait_sec" .= wait) else Nothing
        , if leaseExclusiveKey opts then Just ("exclusive" .= True) else Nothing
        ]

-- | 'lease' that throws.
leaseOrThrow :: Client -> Text -> LeaseOptions -> IO Lease
leaseOrThrow c s = orThrow . lease c s

newtype Renewed = Renewed (Maybe UTCTime)

instance FromJSON Renewed where
  parseJSON = withObject "Renewed" $ \o -> Renewed <$> o .:? "expires_at"

-- | Extend an open lease so a long job keeps its key. 'Nothing' asks for the
-- server default (120 seconds); the server caps it at 24 hours. Returns the
-- lease with its new expiry. Fails with 'LeaseNotFound' when the lease is no
-- longer open.
renew :: Client -> Lease -> Maybe Int -> IO (Either AcnologiaError Lease)
renew c l ttl = do
  let body = object (maybe [] (\t -> ["ttl_sec" .= t]) (mfilter' (> 0) ttl))
  r <- callJson c (clientTimeout c) "POST" ("/api/v1/leases/" <> encodeSegment (leaseId l) <> "/renew") (Just body)
  pure $ case r of
    Left e -> Left e
    Right (Renewed (Just t)) -> Right l {leaseExpiresAt = Just t}
    Right (Renewed Nothing) -> Left (BadResponse "renew response has no expires_at")
  where
    mfilter' p m = case m of
      Just v | p v -> Just v
      _ -> Nothing

-- | 'renew' that throws.
renewOrThrow :: Client -> Lease -> Maybe Int -> IO Lease
renewOrThrow c l = orThrow . renew c l

-- | How using a key went.
data Outcome
  = OutcomeOk
  | OutcomeRateLimited
  | OutcomeBusy
  | OutcomeQuotaExhausted
  | OutcomeInvalid
  | OutcomeBanned
  | OutcomeError
  deriving (Eq, Show, Enum, Bounded)

-- | The wire name: @ok@, @rate_limited@, @busy@, @quota_exhausted@,
-- @invalid@, @banned@ or @error@.
outcomeText :: Outcome -> Text
outcomeText = \case
  OutcomeOk -> "ok"
  OutcomeRateLimited -> "rate_limited"
  OutcomeBusy -> "busy"
  OutcomeQuotaExhausted -> "quota_exhausted"
  OutcomeInvalid -> "invalid"
  OutcomeBanned -> "banned"
  OutcomeError -> "error"

-- | What to tell the server in 'report'.
data Report = Report
  { reportOutcome :: Outcome
  , reportStatusCode :: Maybe Int
    -- ^ The provider's HTTP status.
  , reportRetryAfterSec :: Maybe Double
    -- ^ How long the key should cool down, rounded up to whole seconds.
  , reportMessage :: Maybe Text
    -- ^ A short note. The lease secret is removed and it is cut to 500 characters.
  , reportRotate :: Bool
    -- ^ Ask for the next lease in the same call.
  }
  deriving (Eq, Show)

-- | A report with only an outcome.
outcomeReport :: Outcome -> Report
outcomeReport o = Report o Nothing Nothing Nothing False

newtype Reported = Reported (Maybe Lease)

instance FromJSON Reported where
  parseJSON = withObject "Reported" $ \o -> Reported <$> o .:? "next"

-- | Report the outcome of using a key. With 'reportRotate' set it returns the
-- next lease when another key is available.
report :: Client -> Lease -> Report -> IO (Either AcnologiaError (Maybe Lease))
report c l rp = do
  r <- callJson c (clientTimeout c) "POST" ("/api/v1/leases/" <> encodeSegment (leaseId l) <> "/report") (Just body)
  pure (fmap (\(Reported n) -> n) r)
  where
    body =
      object . catMaybes $
        [ Just ("outcome" .= outcomeText (reportOutcome rp))
        , Just ("rotate" .= reportRotate rp)
        , ("status_code" .=) <$> reportStatusCode rp
        , (\s -> "retry_after_sec" .= (ceiling s :: Int)) <$> reportRetryAfterSec rp
        , (\m -> "message" .= scrubMessage m (leaseSecret l)) <$> nonEmptyText (reportMessage rp)
        ]
    nonEmptyText = \case
      Just t | not (T.null t) -> Just t
      _ -> Nothing

-- | 'report' that throws.
reportOrThrow :: Client -> Lease -> Report -> IO (Maybe Lease)
reportOrThrow c l = orThrow . report c l

-- | Replace the secret (raw and URL encoded) with @[key]@ and cut the message
-- to 500 characters. Errors from query auth services often carry the full URL,
-- key included, and a report must not copy it to the server.
scrubMessage :: Text -> Text -> Text
scrubMessage msg secret
  | T.null secret = T.take 500 msg
  | otherwise =
      T.take 500
        . T.replace (decodeLenient (HT.urlEncode True (TE.encodeUtf8 secret))) "[key]"
        . T.replace secret "[key]"
        $ msg

authValue :: Lease -> Text
authValue l = case authType a of
  AuthBearer | T.null (authPrefix a) -> "Bearer " <> leaseSecret l
  _ -> authPrefix a <> leaseSecret l
  where
    a = leaseAuth l

authHeaderName :: Lease -> HeaderName
authHeaderName l =
  let n = authName (leaseAuth l)
   in CI.mk (TE.encodeUtf8 (if T.null n then "Authorization" else n))

authQueryName :: Lease -> Text
authQueryName l = let n = authName (leaseAuth l) in if T.null n then "api_key" else n

isQueryAuth :: Lease -> Bool
isQueryAuth l = authType (leaseAuth l) == AuthQuery

-- | Add the secret to request headers, for header and bearer auth. Replaces a
-- header of the same name. Query auth leaves the headers unchanged.
applyHeaders :: Lease -> RequestHeaders -> RequestHeaders
applyHeaders l hdrs
  | isQueryAuth l = hdrs
  | otherwise = filter ((/= name) . fst) hdrs <> [(name, TE.encodeUtf8 (authValue l))]
  where
    name = authHeaderName l

-- | Add the secret to query parameters, for query auth. Replaces a parameter
-- of the same name. Header and bearer auth leave the query unchanged.
applyQuery :: Lease -> [(Text, Text)] -> [(Text, Text)]
applyQuery l q
  | isQueryAuth l = filter ((/= name) . fst) q <> [(name, authValue l)]
  | otherwise = q
  where
    name = authQueryName l

-- | Put the secret on an http-client request, in a header or the query string
-- as the service expects.
applyToRequest :: Lease -> H.Request -> H.Request
applyToRequest l req
  | isQueryAuth l =
      let name = TE.encodeUtf8 (authQueryName l)
          q = filter ((/= name) . fst) (HT.parseQuery (H.queryString req))
       in H.setQueryString (q <> [(name, Just (TE.encodeUtf8 (authValue l)))]) req
  | otherwise = req {H.requestHeaders = applyHeaders l (H.requestHeaders req)}

-- | The provider URL for a path, with the query encoded. For query auth the
-- secret is in it, so keep the result out of logs.
leaseUrl :: Lease -> Text -> [(Text, Text)] -> Text
leaseUrl l path q =
  leaseBaseUrl l <> "/" <> T.dropWhile (== '/') path <> decodeLenient (HT.renderQuery True query)
  where
    query :: Query
    query = HT.queryTextToQuery [(k, Just v) | (k, v) <- applyQuery l q]

-- ---------------------------------------------------------------------------
-- The lease helper

-- | Return one of these from a 'withKey' callback to report the key and move
-- to the next one.
data KeyProblem
  = -- | The provider rate limited the key. Carries the cool down in seconds,
    -- the provider status and a note, each when known.
    KeyRateLimited (Maybe Double) (Maybe Int) (Maybe Text)
  | -- | The key used up its quota.
    KeyQuotaExhausted (Maybe Int) (Maybe Text)
  | -- | The provider rejected the key.
    KeyInvalid (Maybe Int) (Maybe Text)
  deriving (Eq, Show)

keyProblemText :: KeyProblem -> Text
keyProblemText = \case
  KeyRateLimited {} -> "rate limited"
  KeyQuotaExhausted {} -> "quota exhausted"
  KeyInvalid {} -> "invalid key"

problemReport :: KeyProblem -> Bool -> Report
problemReport p rot = case p of
  KeyRateLimited ra sc m -> Report OutcomeRateLimited sc ra m rot
  KeyQuotaExhausted sc m -> Report OutcomeQuotaExhausted sc Nothing m rot
  KeyInvalid sc m -> Report OutcomeInvalid sc Nothing m rot

-- | Options for 'withKey'.
data WithKeyOptions = WithKeyOptions
  { withKeyMaxAttempts :: Int
    -- ^ Keys to try before giving up. Default 5.
  , withKeyLease :: LeaseOptions
    -- ^ Passed to every lease call.
  }
  deriving (Eq, Show)

-- | Five attempts with 'defaultLeaseOptions'.
defaultWithKeyOptions :: WithKeyOptions
defaultWithKeyOptions = WithKeyOptions 5 defaultLeaseOptions

-- | Lease a key, run the callback with it and report every lease taken.
--
-- @Right a@ reports @ok@ and returns @a@. @Left problem@ reports the problem
-- and tries the next key, up to 'withKeyMaxAttempts', then fails with
-- 'RotationExhausted'. An exception reports @error@ (with the secret removed
-- from the note) and is rethrown.
withKey :: Client -> Text -> WithKeyOptions -> (Lease -> IO (Either KeyProblem a)) -> IO (Either AcnologiaError a)
withKey c service opts fn = do
  first <- lease c service (withKeyLease opts)
  either (pure . Left) (go 1 []) first
  where
    maxAttempts = max 1 (withKeyMaxAttempts opts)
    go attempt tried l = do
      let tried' = tried <> [leaseKeyId l]
      result <-
        fn l `catch` \(e :: SomeException) -> do
          _ <-
            report c l (outcomeReport OutcomeError) {reportMessage = Just (T.pack (displayException e))}
              `catch` \(_ :: SomeException) -> pure (Right Nothing)
          throwIO e
      case result of
        Right a -> fmap (const a) <$> report c l (outcomeReport OutcomeOk)
        Left problem
          | attempt >= maxAttempts -> do
              _ <- report c l (problemReport problem False)
              pure (Left (RotationExhausted problem))
          | otherwise -> do
              r <- report c l (problemReport problem True)
              case r of
                Left e -> pure (Left e)
                Right (Just next) -> go (attempt + 1) tried' next
                Right Nothing -> do
                  n <- lease c service (withKeyLease opts) {leaseExcludeKeyIds = leaseExcludeKeyIds (withKeyLease opts) <> tried'}
                  either (pure . Left) (go (attempt + 1) tried') n

-- | 'withKey' that throws.
withKeyOrThrow :: Client -> Text -> WithKeyOptions -> (Lease -> IO (Either KeyProblem a)) -> IO a
withKeyOrThrow c s o = orThrow . withKey c s o

-- ---------------------------------------------------------------------------
-- Me and services

-- | The app behind the token.
data MeApp = MeApp {meAppId :: Int, meAppName :: Text}
  deriving (Eq, Show)

-- | The app's request and budget limits. 'Nothing' means no limit.
data Limits = Limits
  { limitsDailyRequests :: Maybe Int
  , limitsMonthlyRequests :: Maybe Int
  , limitsDailyBudget :: Maybe Double
  , limitsMonthlyBudget :: Maybe Double
  }
  deriving (Eq, Show)

-- | A limit the app has reached and when it resets.
data LimitHit = LimitHit {limitHitLimit :: Text, limitHitResetAt :: Maybe UTCTime}
  deriving (Eq, Show)

-- | What the app used today and this month.
data Usage = Usage
  { usageTodayRequests :: Int
  , usageTodayCost :: Double
  , usageMonthRequests :: Int
  , usageMonthCost :: Double
  , usageLastUsedAt :: Maybe UTCTime
  , usageLimitHit :: Maybe LimitHit
  }
  deriving (Eq, Show)

-- | A service the token may use.
data MeService = MeService
  { meServiceSlug :: Text
  , meServiceName :: Text
  , meServiceProvider :: Text
  , meServiceCategory :: Text
  , meServiceAvailable :: Int
    -- ^ For an ordinary app 1 or 0; for a share's token the keys free now.
  , meServiceLogoUrl :: Maybe Text
    -- ^ The service's logo as a @data:@ image URI, when it has one.
  }
  deriving (Eq, Show)

-- | The share a share token belongs to.
data Share = Share
  { shareId :: Text
  , shareName :: Text
  , shareOwnerName :: Text
  , shareOwnerId :: Text
  , shareRecipient :: Text
  , shareLabel :: Text
  , shareStatus :: Text
    -- ^ For example @active@, @paused@, @expired@ or @revoked@.
  , shareMode :: Text
  , shareEndsAt :: Maybe UTCTime
  }
  deriving (Eq, Show)

-- | The answer from @GET /api/v1/me@.
data Me = Me
  { meApp :: MeApp
  , meLimits :: Limits
  , meUsage :: Usage
  , meLimitHit :: Maybe LimitHit
  , meServices :: [MeService]
  , meShare :: Maybe Share
    -- ^ 'Nothing' for an ordinary app.
  , meRaw :: Value
    -- ^ The whole JSON answer, for fields this SDK does not model.
  }
  deriving (Eq, Show)

instance FromJSON MeApp where
  parseJSON = withObject "MeApp" $ \o -> MeApp <$> o .:? "id" .!= 0 <*> o .:? "name" .!= ""

instance FromJSON Limits where
  parseJSON = withObject "Limits" $ \o ->
    Limits <$> o .:? "daily_requests" <*> o .:? "monthly_requests" <*> o .:? "daily_budget" <*> o .:? "monthly_budget"

instance FromJSON LimitHit where
  parseJSON = withObject "LimitHit" $ \o -> LimitHit <$> o .:? "limit" .!= "" <*> o .:? "reset_at"

instance FromJSON Usage where
  parseJSON = withObject "Usage" $ \o ->
    Usage
      <$> o .:? "today_requests" .!= 0
      <*> o .:? "today_cost" .!= 0
      <*> o .:? "month_requests" .!= 0
      <*> o .:? "month_cost" .!= 0
      <*> o .:? "last_used_at"
      <*> o .:? "limit_hit"

instance FromJSON MeService where
  parseJSON = withObject "MeService" $ \o ->
    MeService
      <$> o .:? "slug" .!= ""
      <*> o .:? "name" .!= ""
      <*> o .:? "provider" .!= ""
      <*> o .:? "category" .!= ""
      <*> (availableCount <$> o .:? "available" .!= Null)
      <*> o .:? "logo_url"

instance FromJSON Share where
  parseJSON = withObject "Share" $ \o ->
    Share
      <$> o .:? "id" .!= ""
      <*> o .:? "name" .!= ""
      <*> o .:? "owner_name" .!= ""
      <*> o .:? "owner_id" .!= ""
      <*> o .:? "recipient" .!= ""
      <*> o .:? "label" .!= ""
      <*> o .:? "status" .!= ""
      <*> o .:? "mode" .!= ""
      <*> o .:? "ends_at"

instance FromJSON Me where
  parseJSON v = flip (withObject "Me") v $ \o ->
    Me
      <$> o .:? "app" .!= MeApp 0 ""
      <*> o .:? "limits" .!= Limits Nothing Nothing Nothing Nothing
      <*> o .:? "usage" .!= Usage 0 0 0 0 Nothing Nothing
      <*> o .:? "limit_hit"
      <*> o .:? "services" .!= []
      <*> o .:? "share"
      <*> pure v

availableCount :: Value -> Int
availableCount = \case
  Bool b -> if b then 1 else 0
  Number n -> truncate n
  _ -> 0

-- | What this token may use and how much of its limits it spent
-- (@GET /api/v1/me@).
me :: Client -> IO (Either AcnologiaError Me)
me c = callJson c (clientTimeout c) "GET" "/api/v1/me" Nothing

-- | 'me' that throws.
meOrThrow :: Client -> IO Me
meOrThrow = orThrow . me

-- | A service from @GET /api/v1/services@.
data Service = Service
  { serviceSlug :: Text
  , serviceName :: Text
  , serviceProvider :: Text
  , serviceCategory :: Text
  , serviceAvailable :: Bool
  , serviceBaseUrl :: Text
  , serviceAuth :: Auth
  , serviceGuideUrl :: Text
    -- ^ Where the usage guide is served ('guide' and 'guideMarkdown' fetch it).
  }
  deriving (Eq, Show)

instance FromJSON Service where
  parseJSON = withObject "Service" $ \o ->
    Service
      <$> o .:? "slug" .!= ""
      <*> o .:? "name" .!= ""
      <*> o .:? "provider" .!= ""
      <*> o .:? "category" .!= ""
      <*> ((> 0) . availableCount <$> o .:? "available" .!= Null)
      <*> o .:? "base_url" .!= ""
      <*> o .:? "auth" .!= Auth AuthHeader "" ""
      <*> o .:? "guide_url" .!= ""

newtype ServiceList = ServiceList [Service]

instance FromJSON ServiceList where
  parseJSON = withObject "ServiceList" $ \o -> ServiceList <$> o .:? "services" .!= []

-- | The services this token may use.
services :: Client -> IO (Either AcnologiaError [Service])
services c = fmap (\(ServiceList s) -> s) <$> callJson c (clientTimeout c) "GET" "/api/v1/services" Nothing

-- | 'services' that throws.
servicesOrThrow :: Client -> IO [Service]
servicesOrThrow = orThrow . services

-- ---------------------------------------------------------------------------
-- Usage guides

-- | A service's usage guide (@GET /api/v1/services/{slug}/guide@).
data Guide = Guide
  { guideText :: Text
    -- ^ The guide as Markdown.
  , guideGenerated :: Bool
    -- ^ True when no guide was written for the provider and this one was
    -- generated from its spec.
  , guideStructured :: Value
    -- ^ The structured guide, as JSON.
  }
  deriving (Eq, Show)

instance FromJSON Guide where
  parseJSON = withObject "Guide" $ \o ->
    Guide
      <$> o .:? "markdown" .!= ""
      <*> o .:? "generated" .!= False
      <*> o .:? "guide" .!= Null

guidePath :: Text -> Text
guidePath slug = "/api/v1/services/" <> encodeSegment slug <> "/guide"

-- | How to call a service: the Markdown, whether it was generated, and the
-- structured guide. 'ServiceNotAllowed' for a service the token may not use.
guide :: Client -> Text -> IO (Either AcnologiaError Guide)
guide c slug
  | T.null slug = pure (Left (InvalidInput "service is required"))
  | otherwise = callJson c (clientTimeout c) "GET" (guidePath slug) Nothing

-- | 'guide' that throws.
guideOrThrow :: Client -> Text -> IO Guide
guideOrThrow c = orThrow . guide c

-- | The same guide as Markdown only (@Accept: text/markdown@).
guideMarkdown :: Client -> Text -> IO (Either AcnologiaError Text)
guideMarkdown c slug
  | T.null slug = pure (Left (InvalidInput "service is required"))
  | otherwise = callText c True "text/markdown" (guidePath slug)

-- | 'guideMarkdown' that throws.
guideMarkdownOrThrow :: Client -> Text -> IO Text
guideMarkdownOrThrow c = orThrow . guideMarkdown c

-- | GET a route and return the 2xx body as text. With 'False' the token is
-- not sent, for the public routes.
callText :: Client -> Bool -> BS.ByteString -> Text -> IO (Either AcnologiaError Text)
callText c withToken accept path = do
  let base = if withToken then authHeaders c else publicHeaders c
  r <- send c (clientTimeout c) "GET" (clientBaseUrl c <> path) (base <> [("Accept", accept)]) Nothing
  case r of
    Left e -> pure (Left e)
    Right resp -> do
      let status = HT.statusCode (H.responseStatus resp)
      if status >= 200 && status < 300
        then pure (Right (decodeLenient (LBS.toStrict (H.responseBody resp))))
        else do
          now <- getCurrentTime
          pure (Left (decodeError now (secretsOf c) (H.responseStatus resp) (H.responseHeaders resp) (H.responseBody resp)))

-- | The SDK's headers without the token.
publicHeaders :: Client -> RequestHeaders
publicHeaders c = filter ((/= "Authorization") . fst) (authHeaders c)

-- ---------------------------------------------------------------------------
-- Capabilities

-- | A route of a capability: a service that can do the job.
data CapabilityRoute = CapabilityRoute
  { routeService :: Text
  , routeMapped :: Bool
    -- ^ True when the route maps the answer to the capability's own fields.
  }
  deriving (Eq, Show)

instance FromJSON CapabilityRoute where
  parseJSON = withObject "CapabilityRoute" $ \o ->
    CapabilityRoute <$> o .:? "service" .!= "" <*> o .:? "mapped" .!= False

-- | One job several providers can do, behind one slug.
data Capability = Capability
  { capabilitySlug :: Text
  , capabilityName :: Text
  , capabilityDescription :: Text
  , capabilityStrategy :: Text
  , capabilityParams :: Value
    -- ^ The params it takes, as JSON.
  , capabilityRoutes :: [CapabilityRoute]
  , capabilityUrl :: Text
    -- ^ Where to call it: @{server}/proxy/{slug}@.
  }
  deriving (Eq, Show)

instance FromJSON Capability where
  parseJSON = withObject "Capability" $ \o ->
    Capability
      <$> o .:? "slug" .!= ""
      <*> o .:? "name" .!= ""
      <*> o .:? "description" .!= ""
      <*> o .:? "strategy" .!= ""
      <*> o .:? "params" .!= Null
      <*> o .:? "routes" .!= []
      <*> o .:? "url" .!= ""

newtype CapabilityList = CapabilityList [Capability]

instance FromJSON CapabilityList where
  parseJSON = withObject "CapabilityList" $ \o -> CapabilityList <$> o .:? "capabilities" .!= []

-- | The capabilities this token may call (@GET /api/v1/capabilities@).
-- Empty for a share's token.
capabilities :: Client -> IO (Either AcnologiaError [Capability])
capabilities c = fmap (\(CapabilityList l) -> l) <$> callJson c (clientTimeout c) "GET" "/api/v1/capabilities" Nothing

-- | 'capabilities' that throws.
capabilitiesOrThrow :: Client -> IO [Capability]
capabilitiesOrThrow = orThrow . capabilities

-- | Options for 'capability'.
data CapabilityOptions = CapabilityOptions
  { capService :: Maybe Text
    -- ^ Adds @?_service=<slug>@: use only that service's route.
  , capRaw :: Bool
    -- ^ Adds @?_raw=1@: the provider's own body, not mapped.
  , capIdempotencyKey :: Maybe Text
    -- ^ Sent as @Idempotency-Key@.
  , capNoCache :: Bool
    -- ^ Sends @Cache-Control: no-cache@.
  }
  deriving (Eq, Show)

-- | Any route, mapped answer, no idempotency key, cache allowed.
defaultCapabilityOptions :: CapabilityOptions
defaultCapabilityOptions = CapabilityOptions Nothing False Nothing False

-- | Call a capability: @POST /proxy/{slug}@ with the params as the JSON body.
-- Answers like 'proxy', and 'acnCapability', 'acnService' and 'acnTried' say
-- who served it. A missing param is @bad_params@, and no usable route
-- @no_route@ or @no_route_worked@, as an 'ApiError'.
capability :: Client -> Text -> Value -> CapabilityOptions -> IO (Either AcnologiaError Response)
capability c slug params opts
  | T.null slug = pure (Left (InvalidInput "capability is required"))
  | otherwise =
      sendProxy c (clientBaseUrl c <> "/proxy/" <> encodeSegment slug) $
        (withJsonBody body (proxyRequest "POST" ""))
          { proxyQuery =
              maybe [] (\s -> [("_service", s)]) (capService opts) <> [("_raw", "1") | capRaw opts]
          , proxyIdempotencyKey = capIdempotencyKey opts
          , proxyNoCache = capNoCache opts
          }
  where
    body = case params of
      Null -> object []
      v -> v

-- | 'capability' that throws.
capabilityOrThrow :: Client -> Text -> Value -> CapabilityOptions -> IO Response
capabilityOrThrow c s p = orThrow . capability c s p

-- ---------------------------------------------------------------------------
-- Forward proxy

-- | A host the forward proxy sends to a service.
data ForwardHost = ForwardHost
  { fhHost :: Text
  , fhService :: Text
  , fhName :: Text
  , fhPath :: Text
  }
  deriving (Eq, Show)

instance FromJSON ForwardHost where
  parseJSON = withObject "ForwardHost" $ \o ->
    ForwardHost
      <$> o .:? "host" .!= ""
      <*> o .:? "service" .!= ""
      <*> o .:? "name" .!= ""
      <*> o .:? "path" .!= ""

-- | How to use Acnologia as an HTTP(S) proxy (@GET /api/v1/proxy@). It never
-- holds the token, so 'show' is safe: 'fpProxyUrlWithToken' has an
-- @<app token>@ placeholder.
data ForwardProxy = ForwardProxy
  { fpProxyUrl :: Text
  , fpProxyUrlWithToken :: Text
  , fpCaUrl :: Text
  , fpCaFingerprint :: Text
  , fpPassthrough :: Bool
    -- ^ True when HTTPS to hosts that are no service is tunneled as is.
  , fpHosts :: [ForwardHost]
  , fpEnv :: Map.Map Text Text
    -- ^ The server's suggested environment.
  , fpServiceHeader :: Text
    -- ^ The request header that picks a service.
  }
  deriving (Eq, Show)

instance FromJSON ForwardProxy where
  parseJSON = withObject "ForwardProxy" $ \o ->
    ForwardProxy
      <$> o .:? "proxy_url" .!= ""
      <*> o .:? "proxy_url_with_token" .!= ""
      <*> o .:? "ca_url" .!= ""
      <*> o .:? "ca_fingerprint" .!= ""
      <*> o .:? "passthrough" .!= False
      <*> o .:? "hosts" .!= []
      <*> o .:? "env" .!= Map.empty
      <*> o .:? "service_header" .!= ""

-- | The forward proxy's setup for this token.
forwardProxy :: Client -> IO (Either AcnologiaError ForwardProxy)
forwardProxy c = callJson c (clientTimeout c) "GET" "/api/v1/proxy" Nothing

-- | 'forwardProxy' that throws.
forwardProxyOrThrow :: Client -> IO ForwardProxy
forwardProxyOrThrow = orThrow . forwardProxy

-- | The forward proxy's CA certificate as PEM (@GET /api/v1/proxy/ca.pem@).
-- It is public, so the token is not sent.
forwardProxyCA :: Client -> IO (Either AcnologiaError Text)
forwardProxyCA c = callText c False "application/x-pem-file" "/api/v1/proxy/ca.pem"

-- | 'forwardProxyCA' that throws.
forwardProxyCAOrThrow :: Client -> IO Text
forwardProxyCAOrThrow = orThrow . forwardProxyCA

-- | Fetch the CA and write it to a file (pass @"acnologia-ca.pem"@ for the
-- usual name). A file that cannot be written is 'InvalidInput'.
saveForwardProxyCA :: Client -> FilePath -> IO (Either AcnologiaError ())
saveForwardProxyCA c path = do
  r <- forwardProxyCA c
  case r of
    Left e -> pure (Left e)
    Right pem -> do
      w <- try (BS.writeFile (if null path then "acnologia-ca.pem" else path) (TE.encodeUtf8 pem))
      pure $ case w of
        Left (e :: IOException) -> Left (InvalidInput ("could not write the CA: " <> T.pack (displayException e)))
        Right () -> Right ()

-- | 'saveForwardProxyCA' that throws.
saveForwardProxyCAOrThrow :: Client -> FilePath -> IO ()
saveForwardProxyCAOrThrow c = orThrow . saveForwardProxyCA c

-- | The proxy URL with user @app@ and the client's token as the password,
-- percent escaped: @http://app:acn_...\@host:8888@. It holds the token: keep
-- it out of logs.
forwardProxyURL :: Client -> IO (Either AcnologiaError Text)
forwardProxyURL c = do
  r <- forwardProxy c
  pure $ r >>= \fp -> case T.breakOn "://" (fpProxyUrl fp) of
    (_, "") -> Left (BadResponse "the proxy setup has no proxy_url")
    (scheme, rest0) ->
      let rest = T.drop 3 rest0
          (authority, tailPart) = T.break (`elem` ("/?#" :: String)) rest
          host = snd (T.breakOnEnd "@" authority)
          tokenEnc = decodeLenient (HT.urlEncode True (TE.encodeUtf8 (clientToken c)))
       in Right (scheme <> "://app:" <> tokenEnc <> "@" <> host <> tailPart)

-- | 'forwardProxyURL' that throws.
forwardProxyURLOrThrow :: Client -> IO Text
forwardProxyURLOrThrow = orThrow . forwardProxyURL

-- | The environment that sends an app through the forward proxy:
-- @HTTPS_PROXY@ and @HTTP_PROXY@ ('forwardProxyURL'), @NODE_USE_ENV_PROXY=1@,
-- and @NODE_EXTRA_CA_CERTS@, @REQUESTS_CA_BUNDLE@ and @SSL_CERT_FILE@ set to
-- the CA path (pass @"acnologia-ca.pem"@ for the usual name). The proxy
-- values hold the token: keep them out of logs.
forwardProxyEnv :: Client -> FilePath -> IO (Either AcnologiaError (Map.Map Text Text))
forwardProxyEnv c caPath = fmap env <$> forwardProxyURL c
  where
    ca = T.pack (if null caPath then "acnologia-ca.pem" else caPath)
    env u =
      Map.fromList
        [ ("HTTPS_PROXY", u)
        , ("HTTP_PROXY", u)
        , ("NODE_USE_ENV_PROXY", "1")
        , ("NODE_EXTRA_CA_CERTS", ca)
        , ("REQUESTS_CA_BUNDLE", ca)
        , ("SSL_CERT_FILE", ca)
        ]

-- | 'forwardProxyEnv' that throws.
forwardProxyEnvOrThrow :: Client -> FilePath -> IO (Map.Map Text Text)
forwardProxyEnvOrThrow c = orThrow . forwardProxyEnv c
