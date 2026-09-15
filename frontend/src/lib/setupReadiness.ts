import { API } from "./api";

export type SetupStatus = Awaited<ReturnType<typeof API.GetSetupStatus>>;

/** Pending startup validation is not a missing installation. Stop polling as
 * soon as this screen/request is no longer current. */
export async function waitForSetupStatus(isCurrent: () => boolean, initial?: SetupStatus | null): Promise<SetupStatus | null> {
  let status = initial ?? await API.GetSetupStatus();
  while (isCurrent() && status?.pending) {
    await new Promise((resolve) => window.setTimeout(resolve, 500));
    if (!isCurrent()) return null;
    status = await API.GetSetupStatus();
  }
  return isCurrent() ? status : null;
}
