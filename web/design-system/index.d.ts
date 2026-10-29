import type * as React from 'react';
export type IconName = 'home' | 'store' | 'cart' | 'stock' | 'invoice' | 'card' | 'qris' | 'add' | 'search' | 'print' | 'check' | 'close' | 'lock' | 'amend' | 'left' | 'right' | 'down' | 'sort' | 'more' | 'bell' | 'attach' | 'send' | 'at' | 'copy' | 'delete' | 'edit' | 'eye' | 'download' | 'zoom' | 'image' | 'info' | 'warning' | 'success' | 'error' | 'user' | 'logout' | 'filter' | 'spark' | 'menu' | 'settings' | 'team' | 'sidebar' | 'mic' | 'updown' | 'calendar' | 'open' | 'drag' | 'time' | 'link' | 'apps' | 'chart' | 'dashboard' | 'help' | 'sun' | 'moon' | 'external' | 'refresh' | 'arrowup' | 'arrowdown' | 'funnel' | 'star' | 'switch' | 'pie' | 'line' | 'building' | 'shield' | 'key' | 'plug' | 'globe' | 'palette';
export interface IconProps { name: IconName; size?: number; label?: string; className?: string }
export declare function Icon(props: IconProps): React.ReactElement | null;
export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> { variant?: 'default' | 'primary' | 'quiet' | 'link' | 'danger' | 'danger-solid'; size?: 'sm' | 'md' | 'lg'; icon?: IconName; loading?: boolean }
export declare function Button(props: ButtonProps): React.ReactElement;
export type DocStatus = 'draft' | 'submitted' | 'paid' | 'partly' | 'overdue' | 'cancelled' | 'syncing' | 'instock' | 'lowstock' | 'outstock';
export interface StatusChipProps { status: DocStatus; children?: React.ReactNode; className?: string }
export declare function StatusChip(props: StatusChipProps): React.ReactElement;
export interface AmountProps { value: number; className?: string }
export declare function Amount(props: AmountProps): React.ReactElement;
export declare function formatRupiah(value: number): string;
export interface FieldProps extends React.InputHTMLAttributes<HTMLInputElement> { label: string; hint?: string; error?: string; numeric?: boolean; required?: boolean }
export declare function Field(props: FieldProps): React.ReactElement;
export interface SegmentedProps { label: string; options: { value: string; label: string }[]; value?: string; onChange?: (value: string) => void }
export declare function Segmented(props: SegmentedProps): React.ReactElement;
export interface StatCardProps { label: string; value: number | string; rupiah?: boolean; delta?: string; trend?: 'up' | 'down' }
export declare function StatCard(props: StatCardProps): React.ReactElement;
export interface DataTableColumn { key: string; label: string; numeric?: boolean; rupiah?: boolean; render?: (value: any, row: any) => React.ReactNode }
export interface DataTableProps { columns: DataTableColumn[]; rows: ({ id: string } & Record<string, any>)[]; footer?: Record<string, any>; selected?: string; onSelect?: (row: any) => void }
export declare function DataTable(props: DataTableProps): React.ReactElement;
export interface DocumentHeaderProps { number: string; title: string; status: DocStatus; steps?: boolean }
export declare function DocumentHeader(props: DocumentHeaderProps): React.ReactElement;
export interface PosKeypadProps { initial?: number; offline?: boolean; queued?: number; payLabel?: string; payIcon?: IconName | null }
export declare function PosKeypad(props: PosKeypadProps): React.ReactElement;
export interface RakaProps { size?: number; title?: string; withName?: boolean; company?: string }
export declare function Raka(props: RakaProps): React.ReactElement;
type Node = React.ReactNode;
export interface Option { value: string; label: string; hint?: string; icon?: IconName }
export interface TableColumn extends DataTableColumn { sortable?: boolean }
export interface TableProps { columns: TableColumn[]; rows: ({ id: string } & Record<string, any>)[]; checked?: string[]; sort?: { key: string; dir: 'asc' | 'desc' }; searchPlaceholder?: string; filters?: Node; actions?: Node; bulkActions?: { label: string; icon?: IconName; danger?: boolean }[]; pagination?: Node; card?: boolean; loading?: boolean; loadingRows?: number; emptyText?: string; onOpen?: (row: any) => void }
export declare function Table(props: TableProps): React.ReactElement;
export interface PaginationProps { total: number; page?: number; pageSize?: number; jumper?: boolean; onChange?: (page: number) => void }
export declare function Pagination(props: PaginationProps): React.ReactElement;
export interface DialogProps { title: string; children?: Node; variant?: 'form' | 'confirm' | 'danger'; icon?: IconName; open?: boolean; confirmLabel?: string; cancelLabel?: string; footer?: Node; onClose?: () => void; onConfirm?: () => void }
export declare function Dialog(props: DialogProps): React.ReactElement | null;
export interface DrawerProps { title: string; subtitle?: string; icon?: IconName; headerActions?: Node; footer?: Node; wide?: boolean; open?: boolean; children?: Node; onClose?: () => void }
export declare function Drawer(props: DrawerProps): React.ReactElement | null;
export declare function DrawerSection(props: { title?: string; children?: Node }): React.ReactElement;
export interface CommandItem { label: string; icon?: IconName; sub?: string; kbd?: string; onSelect?: (query: string) => void }
export interface CommandMenuProps { groups: { label: string; items: CommandItem[] }[]; query?: string; placeholder?: string; open?: boolean; onClose?: () => void }
export declare function CommandMenu(props: CommandMenuProps): React.ReactElement | null;
export type MenuItem = '-' | { heading: string } | { label: string; icon?: IconName; kbd?: string; danger?: boolean; disabled?: boolean; onSelect?: () => void };
export interface MenuProps { items: MenuItem[]; float?: boolean; style?: React.CSSProperties; onClose?: () => void }
export declare function Menu(props: MenuProps): React.ReactElement;
export interface ContextMenuProps { items: MenuItem[]; children: Node; at?: { x: number; y: number } }
export declare function ContextMenu(props: ContextMenuProps): React.ReactElement;
export interface AlertProps { tone?: 'info' | 'ok' | 'warn' | 'bad'; title?: string; children?: Node; actions?: Node; onClose?: () => void }
export declare function Alert(props: AlertProps): React.ReactElement;
export declare function Banner(props: { icon?: IconName; children: Node }): React.ReactElement;
export interface ToastProps { title: string; tone?: 'ok' | 'info' | 'warn' | 'bad'; children?: Node; action?: string; onAction?: () => void; onClose?: () => void }
export declare function Toast(props: ToastProps): React.ReactElement;
export declare function Toaster(props: { float?: boolean; children: Node }): React.ReactElement;
export interface SidebarProps { app?: { name: string; type?: string; icon?: IconName; color?: string }; company?: string; outlet?: string; product?: string; groups: { label?: string; items: { id?: string; label: string; icon: IconName; count?: number | null; href?: string }[] }[]; current?: string; user?: string; role?: string; footer?: Node; onNavigate?: (id: string, item: any) => void; onCompany?: () => void }
export declare function Sidebar(props: SidebarProps): React.ReactElement;
export interface TopbarProps { title: string; crumbs?: string[]; actions?: Node; unread?: boolean; searchPlaceholder?: string; onSearch?: () => void; onMenu?: () => void }
export declare function Topbar(props: TopbarProps): React.ReactElement;
export interface TabsProps { tabs: { value: string; label: string; count?: number }[]; value?: string; onChange?: (v: string) => void }
export declare function Tabs(props: TabsProps): React.ReactElement;
export interface CheckboxProps extends React.InputHTMLAttributes<HTMLInputElement> { label?: string; hint?: string; indeterminate?: boolean }
export declare function Checkbox(props: CheckboxProps): React.ReactElement;
export interface RadioGroupProps { name: string; label?: string; options: Option[]; value?: string; onChange?: (v: string) => void }
export declare function RadioGroup(props: RadioGroupProps): React.ReactElement;
export interface SwitchProps { label: string; description?: string; checked?: boolean; onChange?: (on: boolean) => void }
export declare function Switch(props: SwitchProps): React.ReactElement;
export interface TextareaProps extends React.TextareaHTMLAttributes<HTMLTextAreaElement> { label: string; hint?: string }
export declare function Textarea(props: TextareaProps): React.ReactElement;
export interface NativeSelectProps extends React.SelectHTMLAttributes<HTMLSelectElement> { label: string; options: Option[] }
export declare function NativeSelect(props: NativeSelectProps): React.ReactElement;
export interface SelectProps { options: Option[]; label?: string; value?: string; placeholder?: string; searchable?: boolean; defaultOpen?: boolean; onChange?: (v: string) => void }
export declare function Select(props: SelectProps): React.ReactElement;
export interface MultiSelectProps { options: Option[]; label?: string; value?: string[]; placeholder?: string; hint?: string; defaultOpen?: boolean; onChange?: (v: string[]) => void }
export declare function MultiSelect(props: MultiSelectProps): React.ReactElement;
export interface FormProps { children: Node; submitLabel?: string; cancelLabel?: string; note?: string; error?: string; onSubmit?: (e: React.FormEvent) => void; onCancel?: () => void }
export declare function Form(props: FormProps): React.ReactElement;
export declare function FormSection(props: { title: string; description?: string; children: Node }): React.ReactElement;
export interface ChatComposerProps { onSend?: (text: string) => void; value?: string; placeholder?: string; hint?: string; attachments?: { name: string; size?: string; kind?: 'image' | 'file' }[]; mentions?: string[]; mentionOpen?: boolean }
export declare function ChatComposer(props: ChatComposerProps): React.ReactElement;
export interface AvatarProps { name: string; src?: string; size?: number; presence?: 'online' | 'busy' | 'away'; square?: boolean }
export declare function Avatar(props: AvatarProps): React.ReactElement;
export declare function AvatarGroup(props: { names: string[]; max?: number; size?: number }): React.ReactElement;
export interface PreviewImage { name: string; src?: string; fill?: string; size?: string; badge?: string; ratio?: string }
export interface ImagePreviewProps { images: PreviewImage[]; openIndex?: number }
export declare function ImagePreview(props: ImagePreviewProps): React.ReactElement;
export declare function ImageViewer(props: { images: PreviewImage[]; index?: number; open?: boolean; onClose?: () => void }): React.ReactElement | null;
export interface EmptyStateProps { title: string; children?: Node; actions?: Node }
export declare function EmptyState(props: EmptyStateProps): React.ReactElement;
export declare function Kbd(props: { children: Node }): React.ReactElement;
export interface IconButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> { icon: IconName; label: string; dot?: boolean }
export declare function IconButton(props: IconButtonProps): React.ReactElement;
export interface LinkRecord { id: string; title: string; sub?: string; fields?: [string, Node][] }
export interface LinkFieldProps { doctype: string; records: LinkRecord[]; label?: string; value?: string; placeholder?: string; required?: boolean; avatar?: boolean; error?: string; hint?: string; defaultOpen?: boolean; query?: string; onChange?: (record: LinkRecord) => void; onCreate?: () => void; onOpenRecord?: () => void }
export declare function LinkField(props: LinkFieldProps): React.ReactElement;
export interface ChildRow { id: string; item: { id: string; title: string } | null; qty: number; uom: string; rate: number; disc?: number; stock?: number }
export interface ChildTableProps { rows: ChildRow[]; label?: string; tax?: false | { rate: number; dpp: number; label: string }; readOnly?: boolean; onChange?: (rows: ChildRow[]) => void }
export declare function ChildTable(props: ChildTableProps): React.ReactElement;
export interface DatePickerProps { label?: string; value?: string; today?: string; relativeTo?: string; presets?: [string, number][]; holidays?: Record<string, string>; placeholder?: string; error?: string; hint?: string; defaultOpen?: boolean; onChange?: (iso: string) => void }
export declare function DatePicker(props: DatePickerProps): React.ReactElement;
export interface DateRangePickerProps { label?: string; value?: [string, string]; preset?: string; today?: string; holidays?: Record<string, string>; defaultOpen?: boolean; onChange?: (range: [string, string]) => void }
export declare function DateRangePicker(props: DateRangePickerProps): React.ReactElement;
export declare function formatTanggal(iso: string): string;
export interface CardProps { title?: Node; extra?: Node; flush?: boolean; className?: string; style?: React.CSSProperties; children?: Node }
export declare function Card(props: CardProps): React.ReactElement;
export interface DescriptionsProps { items: [Node, Node, number?][]; title?: Node; column?: number; bordered?: boolean }
export declare function Descriptions(props: DescriptionsProps): React.ReactElement;
export interface StepsProps { items: { title: Node; description?: Node; status?: 'wait' | 'process' | 'finish' | 'error' }[]; current?: number; status?: 'process' | 'error'; size?: 'default' | 'small'; label?: string }
export declare function Steps(props: StepsProps): React.ReactElement;
export declare function Breadcrumb(props: { items: string[]; onNavigate?: (index: number) => void }): React.ReactElement;
export interface PageHeaderProps { title: Node; breadcrumb?: string[]; subtitle?: Node; tags?: Node; extra?: Node; tabs?: Node; children?: Node; onBack?: () => void; onNavigate?: (index: number) => void }
export declare function PageHeader(props: PageHeaderProps): React.ReactElement;
export interface AppShellProps { topNav?: Node; sider: Node; header?: Node; pageHeader?: Node; collapsed?: boolean; children?: Node }
export declare function AppShell(props: AppShellProps): React.ReactElement;
export type Tone = 'default' | 'ok' | 'info' | 'warn' | 'bad' | 'new';
export declare function Lozenge(props: { tone?: Tone; bold?: boolean; className?: string; children: Node }): React.ReactElement;
export declare function Badge(props: { appearance?: 'primary' | 'important'; max?: number; label?: string; children: number | string }): React.ReactElement;
export interface AppEntry { id: string; name: string; desc: string; icon: IconName; color: string; external?: boolean }
export interface RecentEntry { name: string; sub?: string; icon?: IconName; [k: string]: any }
export declare function AppSwitcher(props: { apps?: AppEntry[]; current?: string; recent?: RecentEntry[]; float?: boolean; onSelect?: (app: any) => void; onManage?: () => void; onMarket?: () => void }): React.ReactElement;
export interface AccountMenuProps { name: string; email?: string; company?: string; companies?: string[]; theme?: 'light' | 'dark' | 'system'; float?: boolean; onSelect?: (key: string) => void }
export declare function AccountMenu(props: AccountMenuProps): React.ReactElement;
export interface TopNavProps { product?: { name: string; icon: IconName; color: string }; items?: { id?: string; label: string; menu?: boolean }[]; current?: string; createLabel?: string | false; searchPlaceholder?: string; unread?: boolean;
  user?: { name: string; email?: string; company?: string; companies?: string[] }; theme?: 'light' | 'dark' | 'system'; apps?: AppEntry[]; currentApp?: string; recent?: RecentEntry[]; actions?: Node; defaultOpen?: 'switcher' | 'account';
  onHome?: () => void; onNavigate?: (id: string) => void; onCreate?: () => void; onSearch?: () => void; onSwitch?: (app: any) => void; onAccount?: (key: string) => void; onNotifications?: () => void; onHelp?: () => void; onManageApps?: () => void; onMarketplace?: () => void }
export declare function TopNav(props: TopNavProps): React.ReactElement;
export declare function Spinner(props: { size?: 'sm' | 'md' | 'lg'; label?: string }): React.ReactElement;
export declare function Skeleton(props: { variant?: 'text' | 'avatar' | 'number' | 'card'; lines?: number; size?: number; height?: number }): React.ReactElement;
export interface QuickFilter { key: string; label: string; icon?: IconName; options: { value: string; label: string; tone?: Tone; avatar?: boolean; count?: number }[] }
export interface FilterBarProps { filters: QuickFilter[]; value?: Record<string, string[]>; onChange?: (v: Record<string, string[]>) => void; search?: boolean; searchPlaceholder?: string; query?: string; onQuery?: (q: string) => void; more?: Node; extra?: Node; defaultOpen?: string }
export declare function FilterBar(props: FilterBarProps): React.ReactElement;
export interface FilterField { name: string; label: string; type: 'select' | 'link' | 'data' | 'text' | 'currency' | 'int' | 'date' | 'check'; options?: { value: string; label: string }[] }
export interface FilterSpec { match: 'all' | 'any'; rules: { field: string; op: string; value: string }[] }
export declare function FilterBuilder(props: { fields: FilterField[]; value?: FilterSpec; onChange?: (spec: FilterSpec, cel: string) => void; footer?: Node; showCel?: boolean }): React.ReactElement;
export declare function filterCel(spec: FilterSpec, fields: FilterField[]): string;
export declare function SettingsLayout(props: { nav: { label: string; items: { id: string; label: string; icon: IconName }[] }[]; current?: string; onNavigate?: (id: string) => void; navTitle?: string; title?: string; description?: string; children?: Node }): React.ReactElement;
export declare function SettingsSection(props: { title: string; description?: string; extra?: Node; children?: Node }): React.ReactElement;
export declare function SettingsRow(props: { label: string; description?: string; children?: Node }): React.ReactElement;
export interface ChartDatum { label: string; value: number; color?: string; count?: number }
export interface ChartProps { type?: 'bar' | 'line' | 'donut' | 'funnel'; data: ChartDatum[]; rupiah?: boolean; percent?: boolean; height?: number; color?: string; label?: string; totalLabel?: string; emptyText?: string }
export declare function Chart(props: ChartProps): React.ReactElement;
export declare function ChartCard(props: ChartProps & { title: string; subtitle?: string; extra?: Node; loading?: boolean; span?: number; onMenu?: () => void }): React.ReactElement;
export declare function NumberCard(props: { label: string; value: number; rupiah?: boolean; percent?: boolean; compact?: boolean; delta?: string | null; trend?: 'up' | 'down' | 'flat'; goodWhen?: 'up' | 'down'; caption?: string; spark?: number[]; icon?: IconName; loading?: boolean; span?: number; onClick?: () => void }): React.ReactElement;
export declare function ListCard(props: { title: string; subtitle?: string; extra?: Node; columns: TableColumn[]; rows: Record<string, any>[]; loading?: boolean; span?: number; emptyText?: string; onOpen?: (row: any) => void }): React.ReactElement;
export declare function DashGrid(props: { children?: Node }): React.ReactElement;
export declare function Sparkline(props: { data: number[]; width?: number; height?: number; color?: string }): React.ReactElement;
export declare function compactNumber(n: number, rupiah?: boolean): string;
declare global { interface Window { Rangka: Record<string, any> } }
