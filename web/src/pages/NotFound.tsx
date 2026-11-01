import { useLocation } from "react-router-dom";
import { RiArrowLeftLine } from "@remixicon/react";
import { useTitle } from "@/hooks/useTitle";
import { EmptyState } from "@/components/acn/empty-state";
import { RouterButtonLink } from "@/components/acn/button-link";
import { LostPathArt } from "@/components/foundations/illustrations";

export function NotFoundPage() {
  useTitle("Not found");
  const { pathname } = useLocation();
  return (
    <EmptyState
      as="h1"
      art={LostPathArt}
      title="Page not found"
      className="py-16 sm:py-24"
      body={
        <>
          <span dir="ltr" className="mb-2 block font-mono text-caption-1-regular wrap-anywhere text-text-tertiary">
            {pathname}
          </span>
          That page does not exist, or your role cannot open it.
        </>
      }
      action={
        <RouterButtonLink href="/" leadingIcon={RiArrowLeftLine}>
          Back to catalog
        </RouterButtonLink>
      }
    />
  );
}
