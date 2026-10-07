namespace Acme.Orders.Tests;

public class Oversell
{
    [Fact] // rivet:intent ORD-001
    public void CannotOversell() =>
        Assert.Throws<InvalidOperationException>(() => new Checkout().Place(2, 1));
}
