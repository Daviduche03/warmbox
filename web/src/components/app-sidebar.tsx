import { LogoIcon } from "@/components/logo";
import {
	Sidebar,
	SidebarContent,
	SidebarFooter,
	SidebarGroup,
	SidebarHeader,
	SidebarMenu,
	SidebarMenuButton,
	SidebarMenuItem,
} from "@/components/ui/sidebar";
import { NavGroup } from "@/components/nav-group";
import { footerNavLinks, navGroups, withActiveRoute } from "@/components/app-shared";
import { LatestChange } from "@/components/latest-change";
import { useRoute } from "@/lib/router";
import { Plus as PlusIcon } from "@phosphor-icons/react";

export function AppSidebar() {
	const route = useRoute();
	const current = route.path;
	const groups = withActiveRoute(navGroups, current);
	const footers = withActiveRoute([{ items: footerNavLinks }], current)[0].items;

	return (
		<Sidebar collapsible="icon" variant="inset">
			<SidebarHeader className="h-14 justify-center">
				<SidebarMenuButton asChild>
					<a href="/">
						<span className="flex size-[18px] shrink-0 items-center justify-center [&>svg]:size-[18px]">
							<LogoIcon />
						</span>
						<span className="font-medium tracking-tight">warmbox</span>
					</a>
				</SidebarMenuButton>
			</SidebarHeader>
			<SidebarContent>
				<SidebarGroup>
					<SidebarMenuItem className="flex items-center gap-2">
						<SidebarMenuButton
							asChild
							className="min-w-8 bg-primary text-primary-foreground duration-200 ease-linear hover:bg-primary/90 hover:text-primary-foreground active:bg-primary/90 active:text-primary-foreground"
							tooltip="New desktop"
						>
							<a href="/desktops">
								<PlusIcon />
								<span>New desktop</span>
							</a>
						</SidebarMenuButton>
					</SidebarMenuItem>
				</SidebarGroup>
				{groups.map((group, index) => (
					<NavGroup key={`sidebar-group-${index}`} {...group} />
				))}
			</SidebarContent>
			<SidebarFooter>
				<LatestChange />
				<SidebarMenu className={footers.length ? "mt-2" : "hidden"}>
					{footers.map((item) => (
						<SidebarMenuItem key={item.title}>
							<SidebarMenuButton
								asChild
								className="text-muted-foreground"
								isActive={item.isActive}
								size="sm"
							>
								<a href={item.path}>
									{item.icon}
									<span>{item.title}</span>
								</a>
							</SidebarMenuButton>
						</SidebarMenuItem>
					))}
				</SidebarMenu>
			</SidebarFooter>
		</Sidebar>
	);
}
