import { Link } from "react-router-dom";
import { useTitle } from "../hooks/useTitle";
import { Empty } from "../components/ui";

export function NotFoundPage() {
  useTitle("Not found");
  return (
    <Empty
      title="Page not found"
      body="That page does not exist or you do not have access to it."
      action={
        <Link to="/" className="inline-flex h-10 items-center rounded-full bg-ink px-4 text-sm font-medium text-white">
          Back to catalog
        </Link>
      }
    />
  );
}
