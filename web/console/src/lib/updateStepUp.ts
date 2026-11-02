// An update may require a recent passkey proof. Retry only the exact reviewed
// plan, exactly once, on the same operator session.
export async function startUpdateWithStepUp<T>(
  planID: string,
  token: string,
  start: (planID: string) => Promise<T>,
  confirm: (token: string) => Promise<void>,
): Promise<T> {
  try {
    return await start(planID);
  } catch (error) {
    if (!(error instanceof Error) || !("code" in error) || error.code !== "passkey_required") throw error;
    await confirm(token.trim());
    return start(planID);
  }
}
