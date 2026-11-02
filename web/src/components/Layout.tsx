import { useEffect, useRef, useState } from "react";
import { Outlet, useLocation } from "react-router-dom";
import { useAuth } from "@/stores/auth";
import { BRAND_TITLE, TAGLINE } from "@/hooks/useTitle";
import { BRAND_NAME } from "@/components/acn/brand";
import { ACCOUNT_ITEM, isActivePath, navItems, type NavItem } from "@/components/acn/nav";
import { TopBar } from "@/components/acn/top-bar";
import { TabBar } from "@/components/acn/tab-bar";
import { MoreSheet } from "@/components/acn/more-sheet";

// The brand lives in acn/brand; these re-exports keep older imports working.
export { BRAND_NAME, Brand, Logo, Mark } from "@/components/acn/brand";

export function Layout() {
  const role = useAuth((s) => s.user?.role);
  const [more, setMore] = useState(false);
  const location = useLocation();
  const main = useRef<HTMLElement>(null);
  const first = useRef(true);

  const links = navItems(role);
  const primary = links.filter((l) => !l.group);
  const secondary = links.filter((l) => l.group);
  // Phones: the same three in the tab bar, the rest under More with the same groups.
  const overflow: NavItem[] = [...secondary, ACCOUNT_ITEM];
  const moreActive = overflow.some((l) => isActivePath(location.pathname, l.to));

  // While a text field has focus the keyboard is up: hide the tab bar so it does not
  // cover the field (CSS keys off data-typing, on touch screens only).
  useEffect(() => {
    const isText = (el: Element | null) =>
      !!el &&
      (el.tagName === "TEXTAREA" ||
        (el as HTMLElement).isContentEditable ||
        (el.tagName === "INPUT" && !/^(checkbox|radio|button|submit|reset|file|color|range)$/i.test((el as HTMLInputElement).type)));
    const root = document.documentElement;
    const onIn = (e: FocusEvent) => {
      if (isText(e.target as Element)) root.setAttribute("data-typing", "");
    };
    // Focus may move straight to another field, so check after it settles.
    const onOut = () => window.setTimeout(() => !isText(document.activeElement) && root.removeAttribute("data-typing"), 0);
    document.addEventListener("focusin", onIn);
    document.addEventListener("focusout", onOut);
    return () => {
      document.removeEventListener("focusin", onIn);
      document.removeEventListener("focusout", onOut);
      root.removeAttribute("data-typing");
    };
  }, []);

  // Close the sheet on navigation, and move focus to the new page for screen readers.
  useEffect(() => {
    setMore(false);
    if (first.current) {
      first.current = false;
      return;
    }
    window.scrollTo(0, 0);
    main.current?.focus({ preventScroll: true });
  }, [location.pathname]);

  return (
    <div className="flex min-h-dvh flex-col bg-background-full pb-(--tabbar-h)">
      <a href="#main" className="skip-link">
        Skip to content
      </a>
      <TopBar primary={primary} secondary={secondary} />
      <main
        id="main"
        ref={main}
        tabIndex={-1}
        className="mx-auto w-full max-w-[1076px] flex-1 px-4 pt-8 pb-24 outline-none sm:px-6 sm:pt-14"
      >
        <Outlet />
      </main>
      <footer className="border-t border-separator-border">
        <div className="mx-auto flex max-w-[1076px] flex-col gap-1 px-4 py-6 font-mono text-caption-2-regular text-text-tertiary sm:flex-row sm:items-center sm:justify-between sm:px-6">
          <span title={BRAND_TITLE}>
            <span className="text-accent-fg">{BRAND_NAME}</span> · {TAGLINE}
          </span>
          <span>Self-hosted API key pool</span>
        </div>
      </footer>
      <TabBar tabs={primary} moreActive={moreActive} onMore={() => setMore(true)} />
      <MoreSheet open={more} onClose={() => setMore(false)} items={secondary} />
    </div>
  );
}
