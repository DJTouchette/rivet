namespace Acme.Orders;

public class Checkout
{
    public void Place(int requested, int inStock)
    {
        // rivet:intent ORD-001
        if (requested > inStock)
            throw new InvalidOperationException("insufficient stock");
    }
}
