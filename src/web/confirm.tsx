import { useSyncExternalStore } from "react";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "./components/ui/alert-dialog";
type Confirmation = { message: string; action: string; resolve: (approved: boolean) => void };
const pending: Confirmation[] = [];
const listeners = new Set<() => void>();
const emit = () => {
  for (const listener of listeners) listener();
};
export function confirmAction(
  message: string,
  action = /^remove/i.test(message) ? "Remove" : "Delete",
): Promise<boolean> {
  return new Promise((resolve) => {
    pending.push({ message, action, resolve });
    emit();
  });
}
function settle(request: Confirmation, approved: boolean) {
  if (pending[0] !== request) return;
  pending.shift();
  request.resolve(approved);
  emit();
}
export function ConfirmHost() {
  const current = useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => pending[0] || null,
    () => null,
  );
  return (
    <AlertDialog
      open={!!current}
      onOpenChange={(open) => {
        if (!open && current) settle(current, false);
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Confirm {current?.action.toLowerCase()}</AlertDialogTitle>
          <AlertDialogDescription>{current?.message}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel onClick={() => current && settle(current, false)}>Cancel</AlertDialogCancel>
          <AlertDialogAction variant="destructive" onClick={() => current && settle(current, true)}>
            {current?.action}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
