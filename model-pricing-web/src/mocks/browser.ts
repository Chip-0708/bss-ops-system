import { setupWorker } from "msw/browser";
import { supplierReviewHandlers } from "./handlers/supplierReviews";
import { modelHandlers } from "./handlers/models";
import { supplierQuoteHandlers } from "./handlers/supplierQuotes";
import { alertHandlers } from "./handlers/alerts";
import { auditHandlers } from "./handlers/audit";
import { costHandlers } from "./handlers/cost";
import { pricingHandlers } from "./handlers/pricing";
import { pricingPolicyHandlers } from "./handlers/pricingPolicies";
import { supplierHandlers } from "./handlers/suppliers";
import { customerHandlers } from "./handlers/customers";
import { customerQuoteHandlers } from "./handlers/customerQuotes";
import { financeHandlers } from "./handlers/finance";
import { workbenchHandlers } from "./handlers/workbench";
import { orgPermissionHandlers } from "./handlers/orgPermissions";
import { integrationHandlers } from "./handlers/integration";
import { officialPriceHandlers } from "./handlers/officialPrices";
import { supplierPortalHandlers } from "./handlers/supplierPortal";
import { supplierModelApplicationHandlers } from "./handlers/supplierModelApplications";
import { supplierAccountHandlers } from "./handlers/supplierAccount";
import { supplierReconciliationHandlers } from "./handlers/supplierReconciliation";
import { customerPortalHandlers } from "./handlers/customerPortal";
import { changeRequestHandlers } from "./handlers/changeRequests";

export const worker = setupWorker(
  ...supplierReviewHandlers,
  ...modelHandlers,
  ...supplierQuoteHandlers,
  ...alertHandlers,
  ...auditHandlers,
  ...costHandlers,
  ...pricingHandlers,
  ...pricingPolicyHandlers,
  ...supplierHandlers,
  ...customerHandlers,
  ...customerQuoteHandlers,
  ...financeHandlers,
  ...workbenchHandlers,
  ...orgPermissionHandlers,
  ...integrationHandlers,
  ...officialPriceHandlers,
  ...supplierPortalHandlers,
  ...supplierModelApplicationHandlers,
  ...supplierAccountHandlers,
  ...supplierReconciliationHandlers,
  ...customerPortalHandlers,
  ...changeRequestHandlers,
);
