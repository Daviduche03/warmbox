import { Badge } from "@/components/ui/badge";
import {
	Card,
	CardContent,
	CardDescription,
	CardHeader,
	CardTitle,
} from "@/components/ui/card";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import { StatusIndicator } from "@/components/indicator";
import { EmptyState } from "@/components/empty-state";
import { OsMark } from "@/components/os-mark";
import { useStore } from "@/lib/store";

export function ImagesPage() {
	const { images, status } = useStore();
	const defaultImage = status?.default_image;

	return (
		<Card className="shadow-none dark:ring-0">
			<CardHeader className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
				<div className="min-w-0 space-y-2">
					<div className="flex flex-wrap items-center gap-2">
						<CardTitle>Images</CardTitle>
						<Badge variant="secondary">{images.length}</Badge>
					</div>
					<CardDescription>
						Guest images the daemon can boot. The default is used when a request
						doesn't name one.
					</CardDescription>
				</div>
			</CardHeader>
			<CardContent className="p-0">
				{images.length === 0 ? (
					<EmptyState
						hint="Run `warmbox images` on the host to see what is installed."
						title="No images registered"
					/>
				) : (
					<Table>
						<TableHeader>
							<TableRow className="hover:bg-transparent">
								<TableHead className="pl-6">Image</TableHead>
								<TableHead>Status</TableHead>
								<TableHead>Role</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{images.map((name) => {
								const isDefault = name === defaultImage;
								return (
									<TableRow className="h-14 hover:bg-transparent" key={name}>
										<TableCell className="pl-6">
											<span className="flex items-center gap-3 font-medium">
												<span
													aria-hidden="true"
													className="flex size-8 shrink-0 items-center justify-center rounded-md border bg-muted text-muted-foreground [&_svg]:size-4"
												>
													<OsMark className="size-5" name={name} />
												</span>
												<span className="truncate">{name}</span>
											</span>
										</TableCell>
										<TableCell>
											<span className="flex items-center gap-2 text-muted-foreground text-sm">
												<StatusIndicator color="emerald" pulse={false} />
												Available
											</span>
										</TableCell>
										<TableCell>
											<Badge variant={isDefault ? "default" : "outline"}>
												{isDefault ? "Default" : "Opt-in"}
											</Badge>
										</TableCell>
									</TableRow>
								);
							})}
						</TableBody>
					</Table>
				)}
			</CardContent>
		</Card>
	);
}
