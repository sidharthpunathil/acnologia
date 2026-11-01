import { Skeleton } from "@/components/base/skeleton/skeleton";

/** Placeholder for a page: header line, an optional row of tiles, then a grid or list. */
export function PageSkeleton({ variant = "grid", tiles = 0, bare }: { variant?: "grid" | "list"; tiles?: number; bare?: boolean }) {
  return (
    <div role="status" aria-live="polite" aria-busy="true">
      <span className="sr-only">Loading</span>
      {!bare && (
        <div className="mb-8">
          <Skeleton className="h-8 w-64 max-w-full rounded-lg" />
          <Skeleton className="mt-3 h-3 w-48 rounded-md" />
        </div>
      )}
      {tiles > 0 && (
        <div className="mb-8 grid grid-cols-2 gap-2.5 sm:grid-cols-3 lg:grid-cols-6">
          {Array.from({ length: tiles }).map((_, i) => (
            <Skeleton key={i} className="h-[74px] rounded-3xl" />
          ))}
        </div>
      )}
      {variant === "grid" ? (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="flex items-center gap-3.5 rounded-3xl border border-border-button-default bg-background-primary-default p-4">
              <Skeleton className="size-10 rounded-xl" />
              <div className="flex-1">
                <Skeleton className="h-4 w-1/2 rounded-md" />
                <Skeleton className="mt-2 h-3 w-4/5 rounded-md" />
              </div>
            </div>
          ))}
        </div>
      ) : (
        <div className="flex flex-col gap-2.5">
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} className="rounded-3xl border border-border-button-default bg-background-primary-default p-4">
              <Skeleton className="h-4 w-40 rounded-md" />
              <Skeleton className="mt-2 h-3 w-3/4 rounded-md" />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
