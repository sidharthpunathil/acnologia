import { Link } from "react-router-dom";
import { useTitle } from "../hooks/useTitle";
import { Empty } from "../components/ui";

export function NotFoundPage() {
  useTitle("Not found");
  return (
    <Empty
      title="Page not found"
      body="That page does not exist, or your role cannot open it."
      action={
        <Link to="/" className="tap inline-flex h-10 items-center rounded-full bg-ink px-4 text-sm font-medium text-on-ink">
          Back to catalog
        </Link>
      }
    />
  );
}
