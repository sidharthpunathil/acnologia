{-# LANGUAGE OverloadedStrings #-}

-- | Runs proxy, lease, renew, report and me against a real server.
--
-- > ACNOLOGIA_URL=http://127.0.0.1:8080 ACNOLOGIA_TOKEN=acn_... \
-- >   cabal run -fexamples acnologia-smoke -- agi-labs
module Main (main) where

import Acnologia
import Data.Aeson (object, (.=))
import qualified Data.ByteString.Lazy.Char8 as LBC
import Data.Text (Text)
import qualified Data.Text as T
import System.Environment (getArgs)

main :: IO ()
main = do
  args <- getArgs
  let service = maybe "agi-labs" T.pack (case args of (a : _) -> Just a; [] -> Nothing)
  client <- newClientOrThrow defaultConfig
  putStrLn ("client: " <> show client)

  res <- proxyOrThrow client service (withJsonBody (object ["prompt" .= ("hello" :: Text)]) (proxyRequest "POST" "/v1/chat")) {proxyIdempotencyKey = Just "smoke-1"}
  putStrLn ("proxy: " <> show (responseStatus res) <> " " <> LBC.unpack (responseBody res))
  putStrLn ("proxy headers: " <> show (responseAcnologia res))
  again <- proxyOrThrow client service (withJsonBody (object ["prompt" .= ("hello" :: Text)]) (proxyRequest "POST" "/v1/chat")) {proxyIdempotencyKey = Just "smoke-1"}
  putStrLn ("proxy replay: replayed=" <> show (acnReplayed (responseAcnologia again)))

  l <- leaseOrThrow client service defaultLeaseOptions {leaseTtlSec = Just 60}
  putStrLn ("lease: " <> show l)
  putStrLn ("lease url: " <> T.unpack (T.replace (leaseSecret l) "<secret>" (leaseUrl l "/v1/chat" [])))
  putStrLn ("lease headers: " <> show (length (applyHeaders l [])) <> " header(s) set")
  l' <- renewOrThrow client l (Just 300)
  putStrLn ("renew: " <> show (leaseExpiresAt l'))
  next <- reportOrThrow client l' (outcomeReport OutcomeRateLimited) {reportRetryAfterSec = Just 1, reportRotate = True, reportMessage = Just ("429 with " <> leaseSecret l)}
  putStrLn ("report rotate: next=" <> show (fmap leaseKeyId next))
  case next of
    Just n -> reportOrThrow client n (outcomeReport OutcomeOk) >>= \r -> putStrLn ("report ok: " <> show (fmap leaseId r))
    Nothing -> pure ()

  m <- meOrThrow client
  putStrLn ("me: app=" <> show (meApp m) <> " services=" <> show (map meServiceSlug (meServices m)) <> " today=" <> show (usageTodayRequests (meUsage m)))

  bad <- lease client "no-such-service" defaultLeaseOptions
  putStrLn ("unknown service: " <> either show (const "unexpected success") bad)
  gone <- renew client l' Nothing
  putStrLn ("renew after report: " <> either show (const "unexpected success") gone)
