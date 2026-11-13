import type * as React from 'react';
export type IconName = 'home' | 'store' | 'cart' | 'stock' | 'invoice' | 'card' | 'qris' | 'add' | 'search' | 'print' | 'check' | 'close' | 'lock' | 'amend' | 'left' | 'right' | 'down' | 'sort' | 'more' | 'bell' | 'attach' | 'send' | 'at' | 'copy' | 'delete' | 'edit' | 'eye' | 'download' | 'zoom' | 'image' | 'info' | 'warning' | 'success' | 'error' | 'user' | 'logout' | 'filter' | 'spark' | 'menu' | 'settings' | 'team' | 'sidebar' | 'mic' | 'updown' | 'calendar' | 'open' | 'drag' | 'time' | 'link' | 'apps' | 'chart' | 'dashboard' | 'help' | 'sun' | 'moon' | 'external' | 'refresh' | 'arrowup' | 'arrowdown' | 'funnel' | 'star' | 'switch' | 'pie' | 'line' | 'building' | 'shield' | 'key' | 'plug' | 'globe' | 'palette' | 'list' | 'kanban' | 'group' | 'table' | 'fold' | 'unfold' | 'bank' | 'book' | 'cash' | 'history' | 'map' | 'plus' | 'minus' | 'receipt' | 'repeat' | 'tag' | 'truck' | 'undo' | 'wallet';
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
export interface DialogProps { title: string; children?: Node; variant?: 'form' | 'confirm' | 'danger'; icon?: IconName; open?: boolean; confirmLabel?: string; cancelLabel?: string; footer?: Node; headerActions?: Node; className?: string; onClose?: () => void; onConfirm?: () => void }
export declare function Dialog(props: DialogProps): React.ReactElement | null;
export interface DrawerProps { title: string; subtitle?: string; icon?: IconName; headerActions?: Node; footer?: Node; wide?: boolean; open?: boolean; children?: Node; onClose?: () => void }
export declare function Drawer(props: DrawerProps): React.ReactElement | null;
export declare function DrawerSection(props: { title?: string; children?: Node }): React.ReactElement;
export interface CommandItem { label: string; icon?: IconName; sub?: string; kbd?: string; onSelect?: (query: string) => void }
/** ask: false drops the built-in "Tanya Raka" item and the > / @ hints; onQuery gets every keystroke (for a server search).
 *  A group with filter: false is shown as given (its items already match, e.g. server results); others filter by label + sub. */
export interface CommandMenuProps { groups: { label: string; items: CommandItem[]; filter?: boolean }[]; query?: string; placeholder?: string; open?: boolean; ask?: boolean; onQuery?: (q: string) => void; onClose?: () => void }
export declare function CommandMenu(props: CommandMenuProps): React.ReactElement | null;
export type MenuItem = '-' | { heading: string } | { label: string; icon?: IconName; kbd?: string; checked?: boolean; danger?: boolean; disabled?: boolean; onSelect?: () => void };
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
/** items: a parent entry with children (contract 0037), one level deep; it opens when it holds the current page. */
export interface NavItem { id?: string; label: string; icon?: IconName; count?: number | null; href?: string; items?: NavItem[] }
/** Frappe side navigation. collapsed: icons only, labels as title tooltips, width --sidebar-collapsed; the footer stays, compact:
 *  a custom footer renders centred and clipped in .rk-side-foot.collapsed (give it icon-only content), the legacy user shows only the avatar.
 *  Legacy props: app (mapped to switcher, its tile becomes the mark), product (switcher title), company/outlet/onCompany, user/role. */
export interface SidebarProps {
  switcher?: { title: string; subtitle?: Node; mark?: Node; onClick?: () => void };
  search?: { label?: string; onOpen?: () => void };
  pinned?: NavItem[];
  /** collapsed: the group's starting state before the person folds or opens it. */
  groups?: { label?: string; collapsed?: boolean; items: NavItem[] }[];
  /** Groups fold on a click of their label (remembered under foldKey, default rk.nav.folded); a navigation with more
   *  than foldAfter (15) links starts with only the current page's group open. foldGroups: false turns it off. */
  foldGroups?: boolean; foldAfter?: number; foldKey?: string;
  current?: string; onNavigate?: (id: string, item: NavItem) => void;
  collapsed?: boolean; onToggleCollapse?: () => void;
  /** Opened from the tablet-portrait rail: the full sidebar floats over the page (position fixed, shadow). */
  float?: boolean;
  footer?: Node;
  app?: { name: string; type?: string; icon?: IconName; color?: string }; product?: string; company?: string; outlet?: string; onCompany?: () => void; user?: string; role?: string;
}
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
/** Slim 48px page header: "Parent / Title", status pill, actions; tabs underneath.
 *  Legacy aliases: breadcrumb (strings, the last one names this page and is not repeated) + onNavigate(i), tags = status, extra = actions, subTitle / subtitle. */
export interface PageHeaderProps {
  title: Node; breadcrumbs?: { label: Node; onClick?: () => void }[]; status?: Node; actions?: Node; tabs?: Node; onBack?: () => void; children?: Node;
  breadcrumb?: string[]; onNavigate?: (index: number) => void; tags?: Node; extra?: Node; subtitle?: Node; subTitle?: Node;
}
export declare function PageHeader(props: PageHeaderProps): React.ReactElement;
export interface AppShellProps { topNav?: Node; sider: Node; header?: Node; pageHeader?: Node; collapsed?: boolean; children?: Node }
export declare function AppShell(props: AppShellProps): React.ReactElement;
export type Tone = 'default' | 'ok' | 'info' | 'warn' | 'bad' | 'new';
export declare function Lozenge(props: { tone?: Tone; bold?: boolean; className?: string; children: Node }): React.ReactElement;
export declare function Badge(props: { appearance?: 'primary' | 'important'; max?: number; label?: string; children: number | string }): React.ReactElement;
export interface AppEntry { id: string; name: string; desc: string; icon: IconName; color: string; external?: boolean }
export interface RecentEntry { name: string; sub?: string; icon?: IconName; [k: string]: any }
export declare function AppSwitcher(props: { apps?: AppEntry[]; current?: string; recent?: RecentEntry[]; float?: boolean; onSelect?: (app: any) => void; onManage?: () => void; onMarket?: () => void }): React.ReactElement;
export interface AccountMenuSection { heading?: string; items: { key: string; icon: IconName; label: string; on?: boolean }[] }
export interface AccountMenuProps { name: string; email?: string; company?: string; companies?: string[]; theme?: 'light' | 'dark' | 'system'; extra?: AccountMenuSection[]; float?: boolean; onSelect?: (key: string) => void }
export declare function AccountMenu(props: AccountMenuProps): React.ReactElement;
export interface TopNavProps { product?: { name: string; icon: IconName; color: string }; items?: { id?: string; label: string; menu?: boolean }[]; current?: string; createLabel?: string | false; searchPlaceholder?: string; unread?: boolean;
  user?: { name: string; email?: string; company?: string; companies?: string[] }; theme?: 'light' | 'dark' | 'system'; apps?: AppEntry[]; currentApp?: string; recent?: RecentEntry[]; actions?: Node; accountExtra?: AccountMenuSection[]; defaultOpen?: 'switcher' | 'account';
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
/** Breakpoints from tokens.json: phone < 768, tablet portrait 768-1023, tablet 1024-1279, desktop >= 1280. */
export declare const BREAKPOINTS: { tablet: number; tabletLandscape: number; desktop: number };
export type Breakpoint = "phone" | "tablet-portrait" | "tablet" | "desktop";
export declare function breakpoint(width?: number): Breakpoint;
/** React hook: the current breakpoint, updated on resize. */
export declare function useBreakpoint(): Breakpoint;
/** True on phones and touch tablets (controls are 44px there). */
export declare function isTouch(): boolean;
export type ListView = 'list' | 'kanban' | 'group' | 'report';
export declare function ViewSwitcher(props: { value: ListView; options: { id: ListView; label: string; icon?: IconName }[]; onChange?: (id: ListView) => void; label?: string }): React.ReactElement;
/** Renders nothing when count is 0; otherwise "<count> dipilih", the actions (children) and a clear button. */
export declare function BulkBar(props: { count: number; onClear?: () => void; children?: Node }): React.ReactElement | null;
/** Groups from GET /api/v1/aggregate: values like {"sum:grand_total": n}, shown through valueFormat(valueKey, v). A group with an empty
 *  label and a null key reads "(kosong)". disabled (optional) makes a header that cannot be opened, e.g. the "Lainnya" bucket. */
export interface GroupListGroup { key: string | number | boolean | null; label?: string; count: number; values?: Record<string, any>; open?: boolean; rows?: Node; disabled?: boolean }
export declare function GroupList(props: { groups: GroupListGroup[]; valueFormat?: (key: string, v: any) => Node; onToggle?: (key: GroupListGroup['key']) => void }): React.ReactElement;
export interface ReportColumn { field: string; label: string; align?: 'left' | 'right' | 'center'; render?: (value: any, row: Record<string, any>) => Node }
/** Header 32px, rows 36px, a sticky total row (totals.label in the first cell, totals.values[field] already formatted), scrolls inside its own box.
 *  onColumnsChange: each header gets a hide button that calls it with the remaining columns. */
export declare function ReportTable(props: { columns: ReportColumn[]; rows: Record<string, any>[]; totals?: { label: Node; values?: Record<string, Node> }; onColumnsChange?: (columns: ReportColumn[]) => void; emptyText?: string }): React.ReactElement;
/** 280px right panel; wrap the form and the panel in .rk-docpage so the panel moves below the form at <= 1100px. */
export declare function DocPanel(props: { sections: { id?: string; title?: Node; content: Node }[]; label?: string }): React.ReactElement;
/** The Sendi symbol (Bata, or white when reversed) with the "rangka" wordmark; `word: false` shows the symbol alone. */
export declare function Logo(props: { size?: number; reversed?: boolean; word?: boolean; className?: string }): React.ReactElement;
/** Sign-in and other account pages: a Bata brand panel (logo, Sendi joint, headline) beside the form; a strip above it on phones.
 * `brand.text: null` hides the line under the headline. */
export declare function AuthLayout(props: { brand?: { title?: Node; text?: Node | null }; chip?: Node; title?: Node; subtitle?: Node; footer?: Node; children?: Node }): React.ReactElement;
/** ⌘J ask bar: 560px, fixed 24px above the bottom, centred (a full-width sheet on phones). Renders nothing when closed.
 *  Esc calls onClose; Enter calls onSend (Shift+Enter is a newline); blank or busy does not send; both keys are ignored while an IME composes.
 *  Render it as a sibling of ChatDrawer: an open drawer hides the ask bar (the drawer has its own composer). */
export declare function AskBar(props: { open: boolean; context: { label: string } | null; onRemoveContext?: () => void; value: string; onChange: (v: string) => void;
  onSend: () => void; onOpenChat?: () => void; onClose: () => void; busy?: boolean }): React.ReactElement | null;
export type ChangesetRisk = 'low' | 'medium' | 'high' | 'unknown';
export interface ChatChangeset { id: string; title: string; risk: ChangesetRisk; status: string }
export interface ChatTurn { role: 'user' | 'assistant'; text: string; activity?: string[]; changesets?: ChatChangeset[]; escalate?: string; error?: string }
/** 440px chat with Raka on the right, non-modal (no scrim), full screen at 600px and below. The log is aria-live="polite" and scrolls to the newest turn.
 *  The default change set card shows title, risk pill, "Lihat pratinjau" (onPreview) and "Buka di Ubah" (onOpenUbah); renderChangeset replaces it. */
export declare function ChatDrawer(props: { open: boolean; turns: ChatTurn[]; busy?: boolean; onSend: (text: string) => void; onNew?: () => void; onClose: () => void;
/** OS / Raka · Diam | Hover | Tersembunyi | Terbuka: the floating Raka button, 24px from the bottom right. Hover or focus
 *  widens it to "Tanya Raka ⌘J" with the waiting count; its small button tucks it against the right edge. Mount it only
 *  where Raka is on (meta.features.ai === true). */
/** Chat / Format / * (Figma "Raka Chat" › Isi jawaban): Raka's markdown as React elements, never HTML. Headings, bold,
 * italic, `code`, safe links (http(s), mailto, "/path", "#anchor"), lists, quotes, GFM tables (numbers right-aligned),
 * key-values (a list of "**Label:** value"), ``` code blocks with language/file and Salin, callouts
 * (> [!NOTE] | [!TIP] | [!WARNING] | [!CAUTION]). `collapse` folds an answer longer than that many lines. */
export declare function RichText(props: { text: string; collapse?: number; streaming?: boolean; className?: string }): React.ReactElement;
/** Chat / Format / Diagram: a Mermaid diagram drawn as SVG (vendored Mermaid, loaded on first use; call configureDiagrams first).
 * `pending` (a fence still streaming) shows a skeleton, never the source. A bad diagram shows "Diagram belum bisa ditampilkan"
 * with "Lihat sumber". "Perbesar" opens a DiagramViewer, or calls `onOpen` when the host passes it (then the host owns the viewer). RichText draws ```mermaid fences with this. */
export declare function Diagram(props: { source: string; title?: string; alt?: string; pending?: boolean; onOpen?: () => void; className?: string }): React.ReactElement;
/** Chat / DiagramViewer: a zoom and pan popup (scale 25-400%, wheel/pinch/drag, keys + - 0 and arrows). Below 720 px it is full screen and
 * draws the source top-down. `actions` sit in the head next to the title. */
export declare function DiagramViewer(props: { open: boolean; source: string; title: string; actions?: Node; onClose: () => void }): React.ReactElement | null;
/** The first line's direction of a flowchart/graph becomes TD (`flowchart LR` -> `flowchart TD`); other diagram types are unchanged. */
export declare function diagramTopDown(source: string): string;
/** Where Mermaid lives (the host's vendor/mermaid.min.js); onRenderError(source, message) fires once per failing source. */
export declare function configureDiagrams(o: { src: string; onRenderError?: (source: string, error: string) => void }): void;
/** The one memoised promise for the Mermaid global (securityLevel strict, htmlLabels off). Rejects without a configured src. */
export declare function loadMermaid(): Promise<unknown>;
/** One stage of an offer option's flow (contract 0049 §1). `pieces` are display labels only. */
export interface FlowStage { stage: string; label: string; pieces: string[]; state: "covered" | "not_covered" | "later" }
/** A flow as Mermaid text: `flowchart <dir>` (default LR), nodes s0..sN labelled "stage<br/>pieces" (at most 2, then +n; "Belum tercakup" or
 * "Menyusul" for those states), one chain of edges, then `class s<i> <state>`. Labels are escaped (#35; #quot; #lt; #gt; #59;, newline to space). */
export declare function flowToMermaid(flow: FlowStage[], dir?: "LR" | "TD"): string;
export interface OfferOption { id: string; path: "custom" | "modules" | "bundle"; title: string; flow?: FlowStage[];
  records?: string[]; screens?: string[]; raka_does?: string[]; needs_approval?: string[]; adds?: string[];
  not_covered?: Array<{ stage: string; text: string }>; later?: Array<{ piece: string; text: string; reason?: { code: string; message: string } }> }
export interface Offer { id: string; recommended: string; options: OfferOption[] }
/** Chat / OfferCompare (0049 §4): root `[data-card=offer][data-offer=<id>]`, one `section[data-option=<id>]` per option, recommended first
 * with "Disarankan". Equal columns from 720 px of card width, a scroll-snap carousel with "1 / 3" below. "Perbesar" opens a DiagramViewer
 * (LR source) whose head carries `renderActions(option, isRecommended)`. `closedLabel` ("Sudah dijawab", "Peta sudah berubah") mutes the
 * card and drops every action, also in the viewer. `footer` (Ubah sesuatu, Tidak dulu) sits under the row. */
export declare function OfferCompare(props: { offer: Offer; closedLabel?: string; renderActions: (option: OfferOption, isRecommended: boolean) => Node;
  footer?: Node; className?: string }): React.ReactElement;
/** The block list RichText draws (for tests and tools). */
export declare function markdownBlocks(text: string): Array<{ t: "h" | "p" | "list" | "kv" | "table" | "code" | "quote" | "callout" | "hr"; [k: string]: unknown }>;
/** The link a Raka answer may open, or "" (javascript:, data:, //host and the like stay text). */
export declare function safeHref(url: string): string;
export declare function RakaLauncher(props: { open: boolean; pending?: number; tucked?: boolean; onToggle: () => void; onTuck?: (tucked: boolean) => void }): React.ReactElement;
  renderChangeset?: (cs: ChatChangeset) => Node; onPreview?: (id: string) => void; onOpenUbah?: (id: string) => void;
  /** a 440px panel above RakaLauncher instead of a full-height drawer */ floating?: boolean; subtitle?: string; /** the page title as a context chip */ context?: string | null }): React.ReactElement | null;
declare global { interface Window { Rangka: Record<string, any> } }
