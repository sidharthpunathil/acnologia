import { Link, useLocation } from "react-router-dom";
import { Compass } from "lucide-react";
import { useTitle } from "../hooks/useTitle";
import { Empty } from "../components/ui";

export function NotFoundPage() {
  useTitle("Not found");
  const { pathname } = useLocation();
  return (
    <Empty
      icon={<Compass className="h-5 w-5" />}
      title="Page not found"
      className="py-16 sm:py-24"
      body={
        <>
          <span className="wrap-anywhere mb-2 block font-mono text-xs text-faint">{pathname}</span>
          That page does not exist, or your role cannot open it.
        </>
      }
      action={
        <Link
          to="/"
          className="tap inline-flex h-10 items-center rounded-full bg-ink px-4 text-sm font-medium text-on-ink transition-[background-color,transform] duration-150 ease-out hover:bg-ink-strong active:scale-[0.98]"
        >
          Back to catalog
        </Link>
      }
    />
  );
}
