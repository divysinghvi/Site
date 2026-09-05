// Every route is prerendered unless it opts out (trace/[id] does);
// trailingSlash 'never' → dist/index.html, dist/dashboard.html, …
export const prerender = true;
export const trailingSlash = 'never';
