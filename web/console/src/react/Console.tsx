import { useState } from "react";
import { Alert, AlertDescription, AlertTitle } from "$lib/components/ui/alert/react";
import { Badge } from "$lib/components/ui/badge/react";
import { Button } from "$lib/components/ui/button/react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "$lib/components/ui/card/react";
import { Checkbox } from "$lib/components/ui/checkbox/react";
import { Input } from "$lib/components/ui/input/react";
import { Label } from "$lib/components/ui/label/react";
import { RadioGroup, RadioGroupItem } from "$lib/components/ui/radio-group/react";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "$lib/components/ui/select/react";
import { Separator } from "$lib/components/ui/separator/react";
import { Skeleton } from "$lib/components/ui/skeleton/react";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "$lib/components/ui/table/react";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "$lib/components/ui/tooltip/react";
import { Modal } from "./Modal";
import { RowMenu } from "./RowMenu";

export default function Console() {
  const [checked, setChecked] = useState(true);
  const [radio, setRadio] = useState("passkey");
  const [category, setCategory] = useState("access");
  const [modalOpen, setModalOpen] = useState(false);
  const [notice, setNotice] = useState("No menu action selected");

  return (
    <TooltipProvider>
      <main className="mx-auto grid min-h-screen w-full max-w-4xl content-start gap-6 p-6 sm:p-10">
        <header>
          <p className="plate"><b>UI-012</b> · React migration preview</p>
          <h1 className="mt-2 text-2xl font-semibold">Console primitives</h1>
          <p className="text-muted-foreground mt-2 text-sm">Local-only parity check. The embedded Svelte console remains active.</p>
        </header>

        <Alert>
          <AlertTitle>Review the access decision</AlertTitle>
          <AlertDescription>This preview exercises the new React primitives under the console CSP.</AlertDescription>
        </Alert>

        <Card>
          <CardHeader>
            <CardTitle>Authentication</CardTitle>
            <CardDescription>Choose a method and review the native controls.</CardDescription>
          </CardHeader>
          <CardContent className="grid gap-5">
            <div className="grid gap-2">
              <Label htmlFor="preview-email">Email</Label>
              <Input id="preview-email" type="email" defaultValue="operator@example.test" />
            </div>
            <div className="flex items-center gap-2">
              <Checkbox id="preview-remember" checked={checked} onCheckedChange={(value) => setChecked(value === true)} />
              <Label htmlFor="preview-remember">Keep this tab signed in</Label>
            </div>
            <RadioGroup aria-label="Authentication method" value={radio} onValueChange={setRadio} className="flex flex-wrap gap-4">
              <div className="flex items-center gap-2"><RadioGroupItem id="preview-passkey" value="passkey" /><Label htmlFor="preview-passkey">Passkey</Label></div>
              <div className="flex items-center gap-2"><RadioGroupItem id="preview-host" value="host" /><Label htmlFor="preview-host">Host login</Label></div>
            </RadioGroup>
            <div className="grid gap-2">
              <Label htmlFor="preview-category">Activity category</Label>
              <Select value={category} onValueChange={setCategory}>
                <SelectTrigger id="preview-category" aria-label="Activity category"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="access">Access decisions</SelectItem>
                  <SelectItem value="admin">Administrator changes</SelectItem>
                  <SelectItem value="deploy">Site deployments</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-wrap items-center gap-3">
              <Button onClick={() => setModalOpen(true)}>Open dialog</Button>
              <Tooltip><TooltipTrigger asChild><Button variant="outline">Focus for tooltip</Button></TooltipTrigger><TooltipContent>Same-origin positioning with style attributes</TooltipContent></Tooltip>
              <RowMenu label="More preview actions" items={[
                { label: "Open overview", hint: "Review current access", onSelect: () => setNotice("Overview selected") },
                { label: "Remove test route", destructive: true, onSelect: () => setNotice("Test route removal selected") },
              ]} />
              <Badge variant="secondary">Preview only</Badge>
            </div>
            <p aria-live="polite" className="text-muted-foreground text-xs">{notice}</p>
          </CardContent>
        </Card>

        <Modal open={modalOpen} onOpenChange={setModalOpen} title="Preview dialog" intro="Escape closes this dialog and returns focus to its trigger.">
          <div className="grid gap-4">
            <p className="text-sm">The dialog uses a Radix focus trap and keeps the existing Ozal surface styles.</p>
            <Separator />
            <Button onClick={() => setModalOpen(false)}>Done</Button>
          </div>
        </Modal>

        <Card>
          <CardHeader><CardTitle>Recent access</CardTitle><CardDescription>Table and loading primitives.</CardDescription></CardHeader>
          <CardContent>
            <Table>
              <TableHeader><TableRow><TableHead>Address</TableHead><TableHead>Decision</TableHead></TableRow></TableHeader>
              <TableBody>
                <TableRow><TableCell>vault.example.test</TableCell><TableCell><Badge>Allowed</Badge></TableCell></TableRow>
                <TableRow><TableCell>preview.example.test</TableCell><TableCell><Badge variant="outline">Waiting</Badge></TableCell></TableRow>
              </TableBody>
            </Table>
            <Skeleton className="mt-4 h-2 w-24" aria-hidden="true" />
          </CardContent>
        </Card>
      </main>
    </TooltipProvider>
  );
}
