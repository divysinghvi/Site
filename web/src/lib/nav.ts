// Route metadata for the top bar: static, so the root layout needs no data.
export interface NavRoute {
	href: string;
	label: string;
}

export const routes: readonly NavRoute[] = [
	{ href: '/', label: 'Trace' },
	{ href: '/dashboard', label: 'Dashboard' },
	{ href: '/logs', label: 'Logs' },
	{ href: '/uptime', label: 'Uptime' },
	{ href: '/postmortems', label: 'Postmortems' },
	{ href: '/alerts', label: 'Alerts' },
	{ href: '/contact', label: 'Contact' }
];

export function isActive(pathname: string, href: string): boolean {
	if (href === '/') return pathname === '/' || pathname.startsWith('/trace/');
	return pathname === href || pathname.startsWith(href + '/');
}
