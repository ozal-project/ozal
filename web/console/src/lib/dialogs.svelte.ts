// Dialogs opened from the user menu. The shell renders that menu twice (the
// desktop sidebar, hidden on phones, and the phone drawer), so a dialog lives
// once at the console root and the menus only ask for it.
export const dialogs = $state({ passkeys: false });
