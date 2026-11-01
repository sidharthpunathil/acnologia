import {
  AcnAiBuilder,
  AcnApiKey,
  AcnAppToken,
  AcnCategoryImage,
  AcnCategoryLlm,
  AcnCategoryOther,
  AcnCategorySearch,
  AcnCategoryVoice,
  AcnCircuitBreaker,
  AcnEgress,
  AcnKeyLease,
  AcnKeyPool,
  AcnKeyRotate,
  AcnKeyVault,
  AcnProviderSpec,
  AcnProxy,
  AcnQuota,
  AcnRateLimited,
  AcnResponseCache,
  AcnServiceGroup,
} from "./icons";
import type { AcnIcon } from "./base";

export * from "./icons";
export { ACN_STROKE, createIcon, type AcnIcon, type AcnIconProps } from "./base";

/** Every icon in the set with what it stands for, for the gallery and for pickers. */
export const ACN_ICONS: Array<{ name: string; icon: AcnIcon; meaning: string }> = [
  { name: "AcnKeyPool", icon: AcnKeyPool, meaning: "Key pool, the catalog" },
  { name: "AcnApiKey", icon: AcnApiKey, meaning: "One API key" },
  { name: "AcnKeyLease", icon: AcnKeyLease, meaning: "Lease a key" },
  { name: "AcnKeyRotate", icon: AcnKeyRotate, meaning: "Rotate keys or a token" },
  { name: "AcnKeyVault", icon: AcnKeyVault, meaning: "Vault, a secured key" },
  { name: "AcnProxy", icon: AcnProxy, meaning: "Proxy mode" },
  { name: "AcnEgress", icon: AcnEgress, meaning: "Egress proxy" },
  { name: "AcnCircuitBreaker", icon: AcnCircuitBreaker, meaning: "Circuit breaker" },
  { name: "AcnResponseCache", icon: AcnResponseCache, meaning: "Response cache" },
  { name: "AcnServiceGroup", icon: AcnServiceGroup, meaning: "Service group" },
  { name: "AcnQuota", icon: AcnQuota, meaning: "Quota" },
  { name: "AcnRateLimited", icon: AcnRateLimited, meaning: "Rate limited" },
  { name: "AcnProviderSpec", icon: AcnProviderSpec, meaning: "Provider spec" },
  { name: "AcnAiBuilder", icon: AcnAiBuilder, meaning: "AI provider builder" },
  { name: "AcnAppToken", icon: AcnAppToken, meaning: "App and its token" },
  { name: "AcnCategoryLlm", icon: AcnCategoryLlm, meaning: "Category: LLM" },
  { name: "AcnCategorySearch", icon: AcnCategorySearch, meaning: "Category: Search" },
  { name: "AcnCategoryVoice", icon: AcnCategoryVoice, meaning: "Category: Voice" },
  { name: "AcnCategoryImage", icon: AcnCategoryImage, meaning: "Category: Image" },
  { name: "AcnCategoryOther", icon: AcnCategoryOther, meaning: "Category: Other" },
];

const CATEGORY: Record<string, AcnIcon> = {
  llm: AcnCategoryLlm,
  llms: AcnCategoryLlm,
  ai: AcnCategoryLlm,
  search: AcnCategorySearch,
  voice: AcnCategoryVoice,
  audio: AcnCategoryVoice,
  speech: AcnCategoryVoice,
  image: AcnCategoryImage,
  images: AcnCategoryImage,
  vision: AcnCategoryImage,
};

/** The mark for a service or provider category. Unknown and empty categories get Other. */
export function categoryIcon(category: string | undefined | null): AcnIcon {
  return CATEGORY[(category ?? "").trim().toLowerCase()] ?? AcnCategoryOther;
}
