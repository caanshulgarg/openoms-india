// Typical forward-shipment flow:
//
//	c := shiprocket.NewClient(apiUserEmail, apiUserPassword)
//	order, _ := c.CreateOrder(ctx, &shiprocket.CreateOrderRequest{...})
//	awb, _ := c.AssignAWB(ctx, order.ShipmentID.String(), "") // "" = auto courier
//	_, _ = c.GeneratePickup(ctx, order.ShipmentID.String())
//	labelURL, _ := c.GenerateLabel(ctx, order.ShipmentID.String())
//	pdf, _ := c.Download(ctx, labelURL)
//	track, _ := c.TrackByAWB(ctx, awb.AWBCode)
package shiprocket
